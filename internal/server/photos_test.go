package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmanke/goteach-prod/internal/auth"
)

var b64url = base64.RawURLEncoding

// --------------------------------------------------------------- Testbilder

// testJPEG malt ein Bild, dessen Drehung man ansehen kann: die linke Hälfte
// rot, die obere Hälfte zusätzlich aufgehellt. Nach einer Vierteldrehung
// liegt das Rot woanders.
func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := range h {
		for x := range w {
			c := color.RGBA{B: 200, A: 0xFF}

			if x < w/2 {
				c = color.RGBA{R: 200, A: 0xFF}
			}

			if y < h/2 {
				c.G = 120
			}

			img.SetRGBA(x, y, c)
		}
	}

	var buf bytes.Buffer

	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// exifAPP1 baut ein APP1-Segment mit genau einem Feld: Orientation.
func exifAPP1(orient uint16) []byte {
	tiff := make([]byte, 0, 26)
	tiff = append(tiff, 'I', 'I')                     // little endian
	tiff = binary.LittleEndian.AppendUint16(tiff, 42) // Magic
	tiff = binary.LittleEndian.AppendUint32(tiff, 8)  // Offset zu IFD0
	tiff = binary.LittleEndian.AppendUint16(tiff, 1)  // ein Eintrag
	tiff = binary.LittleEndian.AppendUint16(tiff, 0x0112)
	tiff = binary.LittleEndian.AppendUint16(tiff, 3) // SHORT
	tiff = binary.LittleEndian.AppendUint32(tiff, 1) // count
	tiff = binary.LittleEndian.AppendUint16(tiff, orient)
	tiff = append(tiff, 0, 0)                        // Rest des Wertfelds
	tiff = binary.LittleEndian.AppendUint32(tiff, 0) // kein weiteres IFD

	payload := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1}
	app1 = binary.BigEndian.AppendUint16(app1, uint16(len(payload)+2))

	return append(app1, payload...)
}

// jpegWithOrientation schiebt das EXIF-Segment hinter den SOI-Marker —
// genau dort, wo eine Kamera es hinschreibt.
func jpegWithOrientation(t *testing.T, w, h int, orient uint16) []byte {
	t.Helper()

	raw := testJPEG(t, w, h)
	out := append([]byte{}, raw[:2]...)
	out = append(out, exifAPP1(orient)...)

	return append(out, raw[2:]...)
}

// -------------------------------------------------------------- Testzugänge

// clubCredential erzeugt ein frisches Schlüsselpaar und stellt damit ein
// Mitglieder-Token aus, wie es die Vereinsseite ausstellt.
func clubCredential(t *testing.T, sub string) (jwk, token string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	if err != nil {
		t.Fatal(err)
	}

	x := make([]byte, 32)
	y := make([]byte, 32)
	key.X.FillBytes(x)
	key.Y.FillBytes(y)

	jwk = fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`,
		b64url.EncodeToString(x), b64url.EncodeToString(y))

	payload, _ := json.Marshal(map[string]any{
		"iss": auth.MemberIssuer,
		"sub": sub,
		"exp": time.Now().Add(time.Hour).Unix(),
		"k":   b64url.EncodeToString(make([]byte, 32)),
	})

	signing := b64url.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`)) +
		"." + b64url.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])

	if err != nil {
		t.Fatal(err)
	}

	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	return jwk, signing + "." + b64url.EncodeToString(sig)
}

// serviceToken ist der Ausweis aus POST /login (HS256).
func serviceToken(t *testing.T, sub string) string {
	t.Helper()

	return auth.SignHS256([]byte("test-secret"), auth.Claims{
		Sub: sub, Iss: tokenIssuer,
		Iat: time.Now().Unix(), Exp: time.Now().Add(time.Hour).Unix(),
	})
}

// galleryEnv ist eine eingerichtete Galerie mit aktivem Dienst-Login.
func galleryEnv(t *testing.T) map[string]string {
	t.Helper()

	return map[string]string{
		"GOTEACH_PHOTO_DIR": t.TempDir(),
		"AUTH_USERS":        "alice:" + testUserHash,
		"AUTH_JWT_SECRET":   "test-secret",
	}
}

// photoRequest baut eine Anfrage mit Ausweis.
func photoRequest(method, path, token string, body *bytes.Buffer,
	contentType string) *http.Request {
	var req *http.Request

	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", contentType)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	return req
}

// uploadRequest baut einen Multipart-Upload.
func uploadRequest(t *testing.T, token string, data []byte,
	filename, caption string) *http.Request {
	t.Helper()

	var buf bytes.Buffer

	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("photo", filename)

	if err != nil {
		t.Fatal(err)
	}

	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}

	if caption != "" {
		_ = mw.WriteField("caption", caption)
	}

	_ = mw.Close()

	return photoRequest(http.MethodPost, photosPath, token, &buf,
		mw.FormDataContentType())
}

func decodeMeta(t *testing.T, rr *httptest.ResponseRecorder) photoMeta {
	t.Helper()

	var m photoMeta

	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("Antwort kein JSON: %v — Body: %s", err, rr.Body.String())
	}

	return m
}

func decodeList(t *testing.T, rr *httptest.ResponseRecorder) []photoMeta {
	t.Helper()

	var list struct {
		Photos []photoMeta `json:"photos"`
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("Liste kein JSON: %v — Body: %s", err, rr.Body.String())
	}

	return list.Photos
}

// ------------------------------------------------------------------ Zugang

// Die Galerie darf nie offen laufen — auch dann nicht, wenn AUTH_USERS
// fehlt und /analyze deshalb jeden durchlässt.
func TestGalleryNeverRunsOpen(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, photosPath, nil)

	rr := serveEnv(t, req, map[string]string{
		"GOTEACH_PHOTO_DIR": t.TempDir(),
		"AUTH_USERS":        "alice:" + testUserHash,
		"AUTH_JWT_SECRET":   "test-secret",
	})

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, erwartet 401 ohne Ausweis", rr.Code)
	}

	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Error("WWW-Authenticate fehlt")
	}
}

// Ohne jeden konfigurierten Ausweis ist die Galerie fehlkonfiguriert, nicht
// öffentlich.
func TestGalleryWithoutAnyCredentialIsMisconfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, photosPath, nil)

	rr := serveEnv(t, req, map[string]string{
		"GOTEACH_PHOTO_DIR": t.TempDir(),
	})

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("Status = %d, erwartet 500 ohne jeden Ausweis", rr.Code)
	}
}

func TestGalleryOffWithoutPhotoDir(t *testing.T) {
	req := photoRequest(http.MethodGet, photosPath, serviceToken(t, "alice"),
		nil, "")

	rr := serveEnv(t, req, map[string]string{
		"AUTH_USERS":      "alice:" + testUserHash,
		"AUTH_JWT_SECRET": "test-secret",
	})

	if rr.Code != http.StatusNotFound {
		t.Fatalf("Status = %d, erwartet 404 ohne GOTEACH_PHOTO_DIR", rr.Code)
	}
}

// Der Vereins-Token allein muss reichen: kein zweites Passwort für die
// Mitglieder, und der Inhaber landet als Hochladender im Eintrag.
func TestGalleryAcceptsTheClubToken(t *testing.T) {
	jwk, token := clubCredential(t, "uwe")
	env := map[string]string{
		"GOTEACH_PHOTO_DIR": t.TempDir(),
		memberKeyEnv:        jwk,
	}

	rr := serveEnv(t, uploadRequest(t, token, testJPEG(t, 40, 30), "spieltag.jpg", ""), env)

	if rr.Code != http.StatusCreated {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	if meta := decodeMeta(t, rr); meta.Uploader != "uwe" {
		t.Errorf("uploader = %q, erwartet uwe", meta.Uploader)
	}
}

func TestGalleryRejectsAForeignClubToken(t *testing.T) {
	jwk, _ := clubCredential(t, "uwe")
	_, foreign := clubCredential(t, "mallory") // anderes Schlüsselpaar

	req := photoRequest(http.MethodGet, photosPath, foreign, nil, "")

	rr := serveEnv(t, req, map[string]string{
		"GOTEACH_PHOTO_DIR": t.TempDir(),
		memberKeyEnv:        jwk,
	})

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, erwartet 401 für fremd signiertes Token", rr.Code)
	}
}

// Ein kaputter Schlüssel ist Fehlkonfiguration und darf nicht stillschweigend
// auf den zweiten Weg zurückfallen.
func TestGalleryRefusesABrokenMemberKey(t *testing.T) {
	req := photoRequest(http.MethodGet, photosPath, serviceToken(t, "alice"),
		nil, "")

	rr := serveEnv(t, req, map[string]string{
		"GOTEACH_PHOTO_DIR": t.TempDir(),
		"AUTH_USERS":        "alice:" + testUserHash,
		"AUTH_JWT_SECRET":   "test-secret",
		memberKeyEnv:        `{"kty":"EC","crv":"P-256","x":"AA","y":"AA"}`,
	})

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("Status = %d, erwartet 500 bei kaputtem %s", rr.Code, memberKeyEnv)
	}
}

// ------------------------------------------------------------- Rundumlauf

func TestPhotoUploadListFetchDelete(t *testing.T) {
	env := galleryEnv(t)
	token := serviceToken(t, "alice")

	rr := serveEnv(t, uploadRequest(t, token, testJPEG(t, 60, 40),
		"spieltag.jpg", "Runde 3 gegen TriLux"), env)

	if rr.Code != http.StatusCreated {
		t.Fatalf("Upload: Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	meta := decodeMeta(t, rr)

	if !validPhotoID(meta.ID) {
		t.Fatalf("ID = %q, erwartet 16 Hex-Zeichen", meta.ID)
	}

	if meta.Caption != "Runde 3 gegen TriLux" || meta.Name != "spieltag.jpg" {
		t.Errorf("Metadaten falsch: %+v", meta)
	}

	if meta.Width != 60 || meta.Height != 40 {
		t.Errorf("Maße = %dx%d, erwartet 60x40", meta.Width, meta.Height)
	}

	list := decodeList(t, serveEnv(t,
		photoRequest(http.MethodGet, photosPath, token, nil, ""), env))

	if len(list) != 1 || list[0].ID != meta.ID {
		t.Fatalf("Liste = %+v, erwartet genau das hochgeladene Foto", list)
	}

	for _, path := range []string{
		photosPath + "/" + meta.ID,
		photosPath + "/" + meta.ID + "/thumb",
	} {
		rr := serveEnv(t, photoRequest(http.MethodGet, path, token, nil, ""), env)

		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s: Status = %d", path, rr.Code)
		}

		if ct := rr.Header().Get("Content-Type"); ct != "image/jpeg" {
			t.Errorf("GET %s: Content-Type = %q, erwartet image/jpeg", path, ct)
		}

		if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s: nosniff fehlt", path)
		}

		if _, _, err := image.Decode(bytes.NewReader(rr.Body.Bytes())); err != nil {
			t.Errorf("GET %s: kein dekodierbares Bild: %v", path, err)
		}
	}

	// Die Vorschau muss kleiner sein als das Original, sonst spart sie nichts.
	full := serveEnv(t, photoRequest(http.MethodGet,
		photosPath+"/"+meta.ID, token, nil, ""), env).Body.Len()
	thumb := serveEnv(t, photoRequest(http.MethodGet,
		photosPath+"/"+meta.ID+"/thumb", token, nil, ""), env).Body.Len()

	if thumb >= full {
		t.Errorf("Vorschau (%d B) ist nicht kleiner als das Bild (%d B)", thumb, full)
	}

	del := serveEnv(t, photoRequest(http.MethodDelete,
		photosPath+"/"+meta.ID, token, nil, ""), env)

	if del.Code != http.StatusOK {
		t.Fatalf("DELETE: Status = %d, Body: %s", del.Code, del.Body.String())
	}

	if list := decodeList(t, serveEnv(t,
		photoRequest(http.MethodGet, photosPath, token, nil, ""), env)); len(list) != 0 {
		t.Errorf("nach dem Löschen sind noch %d Fotos da", len(list))
	}

	if rr := serveEnv(t, photoRequest(http.MethodGet,
		photosPath+"/"+meta.ID, token, nil, ""), env); rr.Code != http.StatusNotFound {
		t.Errorf("gelöschtes Foto: Status = %d, erwartet 404", rr.Code)
	}
}

// Derselbe Inhalt hat dieselbe ID — ein zweiter Upload ist kein zweites Foto.
func TestPhotoUploadIsIdempotent(t *testing.T) {
	env := galleryEnv(t)
	token := serviceToken(t, "alice")
	data := testJPEG(t, 40, 40)

	first := decodeMeta(t, serveEnv(t, uploadRequest(t, token, data, "a.jpg", ""), env))
	again := serveEnv(t, uploadRequest(t, token, data, "b.jpg", ""), env)

	if again.Code != http.StatusOK {
		t.Fatalf("zweiter Upload: Status = %d, erwartet 200", again.Code)
	}

	if decodeMeta(t, again).ID != first.ID {
		t.Error("gleiche Bytes, verschiedene ID")
	}

	if list := decodeList(t, serveEnv(t,
		photoRequest(http.MethodGet, photosPath, token, nil, ""), env)); len(list) != 1 {
		t.Errorf("Liste hat %d Einträge, erwartet 1", len(list))
	}
}

// Die Zeilenfassung ist das, was der wasm-Client der Vereinsseite liest —
// er trägt bewusst keinen JSON-Parser.
func TestPhotoListAsLines(t *testing.T) {
	env := galleryEnv(t)
	token := serviceToken(t, "alice")

	meta := decodeMeta(t, serveEnv(t, uploadRequest(t, token, testJPEG(t, 60, 40),
		"spieltag.jpg", "Runde 3\tgegen TriLux"), env))

	req := photoRequest(http.MethodGet, photosPath+"?format=lines", token, nil, "")
	rr := serveEnv(t, req, env)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d", rr.Code)
	}

	body := strings.TrimSuffix(rr.Body.String(), "\n")
	fields := strings.Split(body, photoFieldSep)

	if len(fields) != 8 {
		t.Fatalf("Zeile hat %d Felder, erwartet 8: %q", len(fields), body)
	}

	if fields[0] != meta.ID || fields[1] != "spieltag.jpg" || fields[3] != "alice" {
		t.Errorf("Felder falsch: %q", fields)
	}

	// Das Tabulatorzeichen aus der Bildunterschrift darf die Zeile nicht
	// zerlegen: cleanText ersetzt jedes Steuerzeichen durch ein Leerzeichen.
	if strings.ContainsAny(body, "\x1e\t\n") {
		t.Errorf("Steuerzeichen in der Zeile: %q", body)
	}

	if fields[5] != "60" || fields[6] != "40" {
		t.Errorf("Maße = %q x %q, erwartet 60 x 40", fields[5], fields[6])
	}
}

// --------------------------------------------------------------- Ablehnung

func TestPhotoRejectsWhatIsNotAnImage(t *testing.T) {
	env := galleryEnv(t)
	token := serviceToken(t, "alice")

	rr := serveEnv(t, uploadRequest(t, token,
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`),
		"bild.svg", ""), env)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("Status = %d, erwartet 415", rr.Code)
	}

	// Der Hinweis auf HEIC ist die halbe Fehlermeldung: iPhones liefern das
	// von Haus aus, und ohne den Hinweis rät der Hochladende.
	if !strings.Contains(rr.Body.String(), "HEIC") {
		t.Errorf("Fehlermeldung nennt HEIC nicht: %s", rr.Body.String())
	}
}

func TestPhotoRejectsTooLarge(t *testing.T) {
	env := galleryEnv(t)
	token := serviceToken(t, "alice")

	rr := serveEnv(t, uploadRequest(t, token,
		bytes.Repeat([]byte{0xAB}, maxPhotoBytes+1), "riesig.jpg", ""), env)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Status = %d, erwartet 413 — Body: %s", rr.Code, rr.Body.String())
	}
}

func TestPhotoRejectsAnEmptyUpload(t *testing.T) {
	env := galleryEnv(t)

	rr := serveEnv(t, uploadRequest(t, serviceToken(t, "alice"), nil, "leer.jpg", ""), env)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, erwartet 400", rr.Code)
	}
}

func TestPhotoRejectsANonMultipartBody(t *testing.T) {
	env := galleryEnv(t)

	req := photoRequest(http.MethodPost, photosPath, serviceToken(t, "alice"),
		bytes.NewBufferString("{}"), "application/json")

	if rr := serveEnv(t, req, env); rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("Status = %d, erwartet 415", rr.Code)
	}
}

// Keine Zeichenkette aus einer Anfrage darf je in einen Pfad geraten.
//
// Ein Pfad mit ".." kommt gar nicht erst an: http.ServeMux normalisiert ihn
// und antwortet mit einer Umleitung. Alles andere fällt an validPhotoID
// durch. Beiden gemeinsam ist, worauf es ankommt — es kommen keine Bytes
// aus dem Dateisystem zurück.
func TestPhotoPathsCannotEscapeTheDirectory(t *testing.T) {
	env := galleryEnv(t)
	token := serviceToken(t, "alice")

	for _, path := range []string{
		photosPath + "/../../etc/passwd",
		photosPath + "/..%2f..%2fetc%2fpasswd",
		photosPath + "/ABCDEF0123456789", // Großbuchstaben sind keine ID
		photosPath + "/kurz",
		photosPath + "/0123456789abcdef/andere",
		photosPath + "/0123456789abcdef/thumb/mehr",
	} {
		rr := serveEnv(t, photoRequest(http.MethodGet, path, token, nil, ""), env)

		if rr.Code == http.StatusOK {
			t.Errorf("GET %s: Status = 200 — es kamen Bytes zurück", path)
		}

		if rr.Code != http.StatusNotFound && rr.Code != http.StatusMovedPermanently {
			t.Errorf("GET %s: Status = %d, erwartet 404 (oder 301 vom Mux)",
				path, rr.Code)
		}
	}
}

// -------------------------------------------------------------- Löschrechte

func TestPhotoDeleteIsLimitedToTheUploaderAndAdmins(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		"GOTEACH_PHOTO_DIR": dir,
		"AUTH_USERS":        "alice:" + testUserHash,
		"AUTH_JWT_SECRET":   "test-secret",
	}

	meta := decodeMeta(t, serveEnv(t, uploadRequest(t,
		serviceToken(t, "alice"), testJPEG(t, 30, 30), "a.jpg", ""), env))

	foreign := serveEnv(t, photoRequest(http.MethodDelete,
		photosPath+"/"+meta.ID, serviceToken(t, "peter"), nil, ""),
		map[string]string{
			"GOTEACH_PHOTO_DIR": dir,
			"AUTH_USERS":        "alice:" + testUserHash + ",peter:" + testUserHash,
			"AUTH_JWT_SECRET":   "test-secret",
		})

	if foreign.Code != http.StatusForbidden {
		t.Fatalf("fremdes Löschen: Status = %d, erwartet 403", foreign.Code)
	}

	admin := serveEnv(t, photoRequest(http.MethodDelete,
		photosPath+"/"+meta.ID, serviceToken(t, "peter"), nil, ""),
		map[string]string{
			"GOTEACH_PHOTO_DIR":    dir,
			"GOTEACH_PHOTO_ADMINS": "peter",
			"AUTH_USERS":           "alice:" + testUserHash + ",peter:" + testUserHash,
			"AUTH_JWT_SECRET":      "test-secret",
		})

	if admin.Code != http.StatusOK {
		t.Fatalf("Admin-Löschen: Status = %d, Body: %s", admin.Code, admin.Body.String())
	}
}

// ------------------------------------------------------- EXIF und Drehung

// Der wichtigste Datenschutzschritt der Galerie: was abgelegt wird, trägt
// keine Kameradaten mehr — und schon gar keine GPS-Koordinaten.
func TestPhotoStripsExifAndAppliesOrientation(t *testing.T) {
	env := galleryEnv(t)
	token := serviceToken(t, "alice")

	// Querformat 60x40 mit Orientation 6 (Vierteldrehung im Uhrzeigersinn):
	// abgelegt gehört es hochkant, sonst liegt jedes Handyfoto auf der Seite.
	data := jpegWithOrientation(t, 60, 40, 6)

	if got := jpegOrientation(data); got != 6 {
		t.Fatalf("Testbild trägt Orientation %d, erwartet 6", got)
	}

	meta := decodeMeta(t, serveEnv(t,
		uploadRequest(t, token, data, "hochkant.jpg", ""), env))

	if meta.Width != 40 || meta.Height != 60 {
		t.Errorf("Maße = %dx%d, erwartet 40x60 (gedreht)", meta.Width, meta.Height)
	}

	stored := serveEnv(t, photoRequest(http.MethodGet,
		photosPath+"/"+meta.ID, token, nil, ""), env).Body.Bytes()

	if bytes.Contains(stored, []byte("Exif")) {
		t.Error("das abgelegte Bild trägt noch einen EXIF-Block")
	}

	if got := jpegOrientation(stored); got != 1 {
		t.Errorf("abgelegte Orientation = %d, erwartet 1", got)
	}
}

func TestOrientationFallsBackToNormal(t *testing.T) {
	for name, data := range map[string][]byte{
		"leer":      nil,
		"kein JPEG": []byte("nicht mal ansatzweise"),
		"nur SOI":   {0xFF, 0xD8},
		"ohne EXIF": testJPEG(t, 8, 8),
		"kaputtes TIFF": append([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x0A},
			[]byte("Exif\x00\x00XX")...),
	} {
		if got := jpegOrientation(data); got != 1 {
			t.Errorf("%s: Orientation = %d, erwartet 1", name, got)
		}
	}
}

func TestApplyOrientationCoversAllEightCases(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))

	for orient := 1; orient <= 8; orient++ {
		got := applyOrientation(src, orient)
		w, h := got.Bounds().Dx(), got.Bounds().Dy()

		wantW, wantH := 4, 2

		if orient >= 5 {
			wantW, wantH = 2, 4
		}

		if w != wantW || h != wantH {
			t.Errorf("Orientation %d: %dx%d, erwartet %dx%d",
				orient, w, h, wantW, wantH)
		}
	}
}

func TestResizeMaxKeepsAspectAndLeavesSmallImagesAlone(t *testing.T) {
	small := image.NewRGBA(image.Rect(0, 0, 100, 50))

	if got := resizeMax(small, 480); got != image.Image(small) {
		t.Error("ein kleines Bild wurde unnötig neu gerechnet")
	}

	big := image.NewRGBA(image.Rect(0, 0, 1200, 600))
	got := resizeMax(big, 480).Bounds()

	if got.Dx() != 480 || got.Dy() != 240 {
		t.Errorf("Maße = %dx%d, erwartet 480x240", got.Dx(), got.Dy())
	}

	tall := resizeMax(image.NewRGBA(image.Rect(0, 0, 300, 900)), 480).Bounds()

	if tall.Dx() != 160 || tall.Dy() != 480 {
		t.Errorf("Hochformat = %dx%d, erwartet 160x480", tall.Dx(), tall.Dy())
	}
}

// Ein gesetztes GOTEACH_PHOTO_DIR ohne eingehängtes Volume muss den Start
// verweigern — sonst landen die Fotos im Container und sind beim nächsten
// Schlafen der Maschine weg, ohne dass je ein Fehler zu sehen war.
func TestPhotoEnvRefusesAMissingVolume(t *testing.T) {
	t.Setenv("GOTEACH_PHOTO_DIR", "/gibt-es-nicht/photos")

	if err := validatePhotoEnv(); err == nil {
		t.Error("fehlendes Volume beim Start akzeptiert")
	}

	t.Setenv("GOTEACH_PHOTO_DIR", filepath.Join(t.TempDir(), "photos"))

	if err := validatePhotoEnv(); err != nil {
		t.Errorf("eingehängtes Volume abgelehnt: %v", err)
	}

	t.Setenv("GOTEACH_PHOTO_DIR", "")

	if err := validatePhotoEnv(); err != nil {
		t.Errorf("abgeschaltete Galerie beanstandet: %v", err)
	}
}

// ------------------------------------------------------------------- CORS

// Ohne DELETE in Access-Control-Allow-Methods scheitert das Löschen aus dem
// Browser schon am Preflight.
func TestPhotosPreflightAllowsDelete(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, photosPath+"/0123456789abcdef", nil)
	req.Header.Set("Origin", "https://flascheleer-berlin.de")

	rr := serveEnv(t, req, galleryEnv(t))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("Status = %d, erwartet 204", rr.Code)
	}

	if methods := rr.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(
		methods, "DELETE") {
		t.Errorf("Allow-Methods = %q, DELETE fehlt", methods)
	}
}

// /api ist die Betriebsdiagnose: ob die Galerie eingerichtet ist, muss man
// von außen sehen können.
func TestAPIInfoReportsTheGallery(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"aus": nil,
		"an":  {"GOTEACH_PHOTO_DIR": t.TempDir()},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api", nil)

		var info map[string]any

		if err := json.Unmarshal(serveEnv(t, req, env).Body.Bytes(), &info); err != nil {
			t.Fatalf("%s: keine JSON-Antwort: %v", name, err)
		}

		if info["gallery"] != (name == "an") {
			t.Errorf("%s: gallery = %v", name, info["gallery"])
		}
	}
}
