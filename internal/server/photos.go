// Die Fotogalerie des Vereins: hochladen, auflisten, ausliefern, löschen.
//
// Der Zuschnitt ist absichtlich der kleinstmögliche, der die Aufgabe löst.
// Keine Datenbank, kein Server-Zustand, kein Schema: pro Foto liegen drei
// Dateien im Verzeichnis aus GOTEACH_PHOTO_DIR, benannt nach dem SHA-256
// der hochgeladenen Bytes.
//
//	<id>.jpg        das Bild, neu kodiert und ohne EXIF (siehe photoimage.go)
//	<id>.thumb.jpg  die Vorschau, längste Kante 480px
//	<id>.json       wer, wann, wie groß, welche Bildunterschrift
//
// Inhaltsadressiert heißt: derselbe Upload zweimal ergibt einen Eintrag,
// nicht zwei — und die Bytes unter einer ID ändern sich nie, also darf der
// Browser sie ein Jahr lang behalten. Die Liste ist ein Verzeichnis-Listing;
// was einen Neustart überleben muss, sind die Dateien und sonst nichts.
//
// Zwei Dinge, die hier nie passieren: eine Zeichenkette aus einer Anfrage
// landet nie in einem Pfad (die IDs erzeugt der Server, und sie müssen
// 16 Hex-Zeichen sein), und ein Content-Type aus einem Upload wird nie
// übernommen (ausgeliefert wird, was der Server selbst kodiert hat).
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// maxPhotoBytes ist die Grenze pro Upload. Ein Handyfoto liegt bei
	// 3–8 MB; 12 lässt Luft, ohne dass ein Versehen die Platte füllt.
	maxPhotoBytes = 12 << 20

	// thumbMaxEdge ist die längste Kante der Vorschau. Die Übersicht lädt
	// nur diese — das Original erst, wenn jemand ein Bild wirklich ansieht.
	thumbMaxEdge = 480

	// Qualitätsstufen beim Neukodieren.
	photoQuality = 88
	thumbQuality = 78

	// maxPhotos deckelt die Galerie. Kein Kunstwert, nur eine Grenze, die
	// verhindert, dass ein Versehen ein 3-GB-Volume füllt.
	maxPhotos = 2000

	// maxCaptionRunes begrenzt die Bildunterschrift.
	maxCaptionRunes = 200

	// maxNameRunes begrenzt den übernommenen Dateinamen.
	maxNameRunes = 120

	// uploadDeadline ersetzt für den Upload den globalen ReadTimeout von
	// 60 Sekunden (server.go). Der reicht für ein SGF, aber nicht für ein
	// 12-MB-Foto über eine schwache Mobilverbindung — und was dort abbricht,
	// bricht ohne brauchbare Fehlermeldung ab.
	uploadDeadline = 5 * time.Minute

	// photoIDLen ist die Länge der ID in Hex-Zeichen (64 Bit des SHA-256).
	photoIDLen = 16

	photosPath = "/photos"
)

// photoMeta ist der Inhalt von <id>.json und zugleich das, was die Liste
// ausgibt. Ein Foto trägt damit dauerhaft, wer es hochgeladen hat — das ist
// die Grundlage dafür, dass jeder seine eigenen Bilder wieder löschen kann.
type photoMeta struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Caption    string `json:"caption,omitempty"`
	Uploader   string `json:"uploader"`
	UploadedAt string `json:"uploadedAt"`
	Width      int    `json:"w"`
	Height     int    `json:"h"`
	Bytes      int64  `json:"bytes"`
}

// photoDir ist das Verzeichnis der Galerie; leer heißt „Galerie aus".
// Wie die übrige Umgebung pro Anfrage gelesen, damit Tests t.Setenv nutzen.
func photoDir() string {
	return strings.TrimSpace(os.Getenv("GOTEACH_PHOTO_DIR"))
}

// galleryEnabled meldet, ob die Galerie überhaupt konfiguriert ist.
func galleryEnabled() bool {
	return photoDir() != ""
}

// validatePhotoEnv prüft beim Serverstart, dass die Galerie dort landet, wo
// sie hingehört.
//
// Der Fehler, gegen den das schützt, ist der teuerste, den diese Sache
// kennt: Steht GOTEACH_PHOTO_DIR auf "/data/photos", ist das Fly-Volume
// aber nicht eingehängt, dann legt MkdirAll das Verzeichnis munter im
// Container an. Alles funktioniert — bis die Maschine das nächste Mal
// schläft und sämtliche Fotos weg sind, ohne dass irgendwo ein Fehler
// stand. Darum: Das übergeordnete Verzeichnis muss vorher existieren.
// Bei einem eingehängten Volume tut es das, bei einem vergessenen nicht.
func validatePhotoEnv() error {
	dir := photoDir()

	if dir == "" {
		return nil
	}

	parent := filepath.Dir(dir)
	info, err := os.Stat(parent)

	if err != nil || !info.IsDir() {
		return fmt.Errorf(
			"GOTEACH_PHOTO_DIR=%q, aber %q gibt es nicht — ist das Volume "+
				"eingehängt? (fly volumes list; [[mounts]] in fly.toml)",
			dir, parent)
	}

	return nil
}

// photoAdmins sind die Namen, die fremde Fotos löschen dürfen
// (GOTEACH_PHOTO_ADMINS, kommagetrennt). Die einzige Rolle, die es gibt.
func photoAdmins() map[string]bool {
	admins := map[string]bool{}

	for _, name := range strings.Split(os.Getenv("GOTEACH_PHOTO_ADMINS"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			admins[name] = true
		}
	}

	return admins
}

// validPhotoID ist der einzige Weg, auf dem eine Zeichenkette aus einer
// Anfrage in die Nähe eines Dateipfads kommt: genau 16 Kleinbuchstaben-Hex.
// Damit kann kein "..", kein "/" und kein Unicode-Trick entstehen.
func validPhotoID(id string) bool {
	if len(id) != photoIDLen {
		return false
	}

	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}

	return true
}

func photoFile(id string) string { return id + ".jpg" }
func thumbFile(id string) string { return id + ".thumb.jpg" }
func metaFile(id string) string  { return id + ".json" }
func inDir(name string) string   { return filepath.Join(photoDir(), name) }
func galleryOff(w http.ResponseWriter) {
	httpError(w, http.StatusNotFound, "Galerie nicht konfiguriert")
}

// writeFileAtomically schreibt erst daneben und benennt dann um. Ein
// abgebrochener Schreibvorgang hinterlässt so keine halbe Datei, die die
// Liste später als gültigen Eintrag lesen würde.
func writeFileAtomically(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")

	if err != nil {
		return err
	}

	name := tmp.Name()

	defer func() {
		tmp.Close()
		os.Remove(name) // nach erfolgreichem Rename ein No-op
	}()

	if _, err := tmp.Write(data); err != nil {
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(name, path)
}

// readMeta liest einen Eintrag; ein unlesbarer wird übersprungen, nicht
// zum Fehler der ganzen Liste erklärt.
func readMeta(name string) (photoMeta, bool) {
	var m photoMeta

	data, err := os.ReadFile(inDir(name))

	if err != nil || json.Unmarshal(data, &m) != nil {
		return m, false
	}

	if !validPhotoID(m.ID) {
		return m, false
	}

	return m, true
}

// listPhotos liefert alle Einträge, neueste zuerst.
func listPhotos() ([]photoMeta, error) {
	entries, err := os.ReadDir(photoDir())

	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	photos := make([]photoMeta, 0, len(entries))

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		if m, ok := readMeta(e.Name()); ok {
			photos = append(photos, m)
		}
	}

	// Neueste zuerst; bei gleicher Sekunde entscheidet die ID, damit die
	// Reihenfolge zwischen zwei Aufrufen stabil bleibt.
	sort.Slice(photos, func(i, j int) bool {
		if photos[i].UploadedAt != photos[j].UploadedAt {
			return photos[i].UploadedAt > photos[j].UploadedAt
		}

		return photos[i].ID < photos[j].ID
	})

	return photos, nil
}

// handlePhotos bedient GET /photos (Liste) und POST /photos (Upload).
func handlePhotos(w http.ResponseWriter, r *http.Request, who string) {
	if !galleryEnabled() {
		galleryOff(w)

		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		photos, err := listPhotos()

		if err != nil {
			log.Printf("goteach-server: Galerie lesen: %v", err)
			httpError(w, http.StatusInternalServerError, "Galerie nicht lesbar")

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"photos": photos})
	case http.MethodPost:
		uploadPhoto(w, r, who)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		httpError(w, http.StatusMethodNotAllowed, "GET oder POST erwartet")
	}
}

// handlePhotoItem bedient /photos/<id> und /photos/<id>/thumb.
func handlePhotoItem(w http.ResponseWriter, r *http.Request, who string) {
	if !galleryEnabled() {
		galleryOff(w)

		return
	}

	rest := strings.TrimPrefix(r.URL.Path, photosPath+"/")
	id, tail, _ := strings.Cut(rest, "/")

	if !validPhotoID(id) || (tail != "" && tail != "thumb") {
		http.NotFound(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		name := photoFile(id)

		if tail == "thumb" {
			name = thumbFile(id)
		}

		servePhotoFile(w, r, name)
	case http.MethodDelete:
		deletePhoto(w, id, who)
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		httpError(w, http.StatusMethodNotAllowed, "GET oder DELETE erwartet")
	}
}

// servePhotoFile liefert eine der abgelegten JPEG-Dateien aus.
//
// Der Content-Type wird gesetzt, nicht geraten: was hier herausgeht, hat
// dieser Dienst selbst kodiert (photoimage.go), und nosniff verbietet dem
// Browser, sich etwas anderes zu überlegen. http.ServeContent bringt ETag,
// Range und If-Modified-Since mit.
func servePhotoFile(w http.ResponseWriter, r *http.Request, name string) {
	file, err := os.Open(inDir(name))

	if err != nil {
		http.NotFound(w, r)

		return
	}

	defer file.Close()

	info, err := file.Stat()

	if err != nil || info.IsDir() {
		http.NotFound(w, r)

		return
	}

	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("X-Content-Type-Options", "nosniff")
	// Der Name ist der Hash des Inhalts — die Bytes darunter ändern sich nie.
	h.Set("Cache-Control", "private, max-age=31536000, immutable")

	http.ServeContent(w, r, name, info.ModTime(), file)
}

// deletePhoto entfernt ein Foto. Löschen darf, wer es hochgeladen hat, und
// wer in GOTEACH_PHOTO_ADMINS steht.
func deletePhoto(w http.ResponseWriter, id, who string) {
	meta, ok := readMeta(metaFile(id))

	if !ok {
		httpError(w, http.StatusNotFound, "Foto nicht gefunden")

		return
	}

	if meta.Uploader != who && !photoAdmins()[who] {
		httpError(w, http.StatusForbidden,
			"Löschen darf nur, wer das Foto hochgeladen hat")

		return
	}

	// Zuerst die Metadaten: ohne sie ist der Eintrag aus der Liste
	// verschwunden, auch wenn eine der Bilddateien noch herumliegt.
	for _, name := range []string{metaFile(id), thumbFile(id), photoFile(id)} {
		if err := os.Remove(inDir(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("goteach-server: %s löschen: %v", name, err)
			httpError(w, http.StatusInternalServerError, "Löschen fehlgeschlagen")

			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// uploadPhoto nimmt ein Foto entgegen (multipart/form-data, Feld "photo",
// optional "caption").
//
// Gestreamt statt in den Speicher gelesen: uploaded() in server.go liest
// ganze Dateien in den RAM, was für ein 1-MB-SGF richtig und für Fotos
// falsch ist. Der Weg hier ist Netz → Temp-Datei (mit mitlaufendem Hash)
// → dekodieren → neu kodieren → umbenennen.
func uploadPhoto(w http.ResponseWriter, r *http.Request, who string) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))

	if err != nil || mediaType != "multipart/form-data" {
		httpError(w, http.StatusUnsupportedMediaType,
			"multipart/form-data mit dem Feld \"photo\" erwartet")

		return
	}

	if err := os.MkdirAll(photoDir(), 0o755); err != nil {
		log.Printf("goteach-server: Galerie-Verzeichnis: %v", err)
		httpError(w, http.StatusInternalServerError, "Galerie nicht schreibbar")

		return
	}

	if photos, err := listPhotos(); err == nil && len(photos) >= maxPhotos {
		httpError(w, http.StatusInsufficientStorage,
			"Die Galerie ist voll (%d Fotos)", maxPhotos)

		return
	}

	// Der globale ReadTimeout gilt für den ganzen Body und ist für Fotos zu
	// knapp. Nicht jeder ResponseWriter kann das (der Recorder in den Tests
	// etwa nicht) — dann bleibt es beim globalen Wert.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(uploadDeadline))

	reader, err := r.MultipartReader()

	if err != nil {
		httpError(w, http.StatusBadRequest, "Multipart-Daten unlesbar")

		return
	}

	upload, caption, err := receivePhoto(reader)

	if err != nil {
		status := http.StatusBadRequest

		var tooBig *photoTooBigError

		if errors.As(err, &tooBig) {
			status = http.StatusRequestEntityTooLarge
		}

		httpError(w, status, "%s", err)

		return
	}

	defer func() {
		upload.file.Close()
		os.Remove(upload.file.Name())
	}()

	meta, status, err := storePhoto(upload, caption, who)

	if err != nil {
		httpError(w, status, "%s", err)

		return
	}

	writeJSON(w, status, meta)
}

// photoTooBigError trennt „zu groß" (413) von „kaputt" (400).
type photoTooBigError struct{ limit int }

func (e *photoTooBigError) Error() string {
	return fmt.Sprintf("Das Foto ist größer als %d MB", e.limit>>20)
}

// pendingUpload ist ein entgegengenommener, noch nicht geprüfter Upload.
type pendingUpload struct {
	file *os.File
	id   string
	name string
}

// receivePhoto streamt das Feld "photo" in eine Temp-Datei und liest
// nebenbei "caption". Der Hash läuft beim Schreiben mit, damit die Bytes
// nur einmal durch die Hand gehen.
func receivePhoto(reader *multipart.Reader) (pendingUpload, string, error) {
	var (
		upload  pendingUpload
		caption string
	)

	for {
		part, err := reader.NextPart()

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			upload.cleanup()

			return pendingUpload{}, "", errors.New("Multipart-Daten unlesbar")
		}

		// Nur das erste "photo" zählt; ein zweites wird verworfen, statt das
		// erste stillschweigend zu ersetzen.
		if part.FormName() == "photo" && upload.file == nil {
			upload, err = streamToTemp(part)
		} else if part.FormName() == "caption" {
			text, _ := io.ReadAll(io.LimitReader(part, 4<<10))
			caption = cleanText(string(text), maxCaptionRunes)
		}

		part.Close()

		if err != nil {
			return pendingUpload{}, "", err
		}
	}

	if upload.file == nil {
		return pendingUpload{}, "", errors.New("Kein Foto im Feld \"photo\"")
	}

	return upload, caption, nil
}

func (u pendingUpload) cleanup() {
	if u.file != nil {
		u.file.Close()
		os.Remove(u.file.Name())
	}
}

// streamToTemp schreibt einen Multipart-Teil in eine Temp-Datei neben dem
// Ziel (damit das spätere Umbenennen im selben Dateisystem bleibt) und
// bricht ab, sobald die Grenze überschritten ist — nicht erst danach.
func streamToTemp(part *multipart.Part) (pendingUpload, error) {
	tmp, err := os.CreateTemp(photoDir(), ".upload-*")

	if err != nil {
		return pendingUpload{}, errors.New("Galerie nicht schreibbar")
	}

	upload := pendingUpload{
		file: tmp,
		name: cleanText(filepath.Base(part.FileName()), maxNameRunes),
	}

	sum := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, sum),
		io.LimitReader(part, maxPhotoBytes+1))

	if err != nil {
		upload.cleanup()

		return pendingUpload{}, errors.New("Upload abgebrochen")
	}

	if written > maxPhotoBytes {
		upload.cleanup()

		return pendingUpload{}, &photoTooBigError{limit: maxPhotoBytes}
	}

	if written == 0 {
		upload.cleanup()

		return pendingUpload{}, errors.New("Kein Foto im Feld \"photo\"")
	}

	upload.id = hex.EncodeToString(sum.Sum(nil))[:photoIDLen]

	if upload.name == "" || upload.name == "." {
		upload.name = upload.id + ".jpg"
	}

	return upload, nil
}

// storePhoto prüft, dreht, verkleinert und legt ab. Der zweite Rückgabewert
// ist der HTTP-Status: 201 für ein neues Foto, 200 wenn dasselbe Bild schon
// in der Galerie liegt — derselbe Inhalt hat dieselbe ID, und ein zweiter
// Upload ist dann kein zweites Foto.
func storePhoto(
	upload pendingUpload, caption, who string,
) (photoMeta, int, error) {
	none := photoMeta{}

	if meta, ok := readMeta(metaFile(upload.id)); ok {
		return meta, http.StatusOK, nil
	}

	if _, err := upload.file.Seek(0, io.SeekStart); err != nil {
		return none, http.StatusInternalServerError,
			errors.New("Upload nicht lesbar")
	}

	img, err := decodePhoto(upload.file)

	if err != nil {
		// Die häufigste Ursache hat einen Namen, und ihn zu nennen erspart
		// dem Hochladenden das Raten: iPhones speichern von Haus aus HEIC.
		return none, http.StatusUnsupportedMediaType, errors.New(
			"Das ist kein lesbares JPEG oder PNG. Fotos vom iPhone liegen oft " +
				"als HEIC vor — in den Kameraeinstellungen \"Maximale " +
				"Kompatibilität\" wählen und noch einmal versuchen")
	}

	full, err := encodeJPEG(img, photoQuality)

	if err != nil {
		return none, http.StatusInternalServerError,
			errors.New("Bild nicht kodierbar")
	}

	thumb, err := encodeJPEG(resizeMax(img, thumbMaxEdge), thumbQuality)

	if err != nil {
		return none, http.StatusInternalServerError,
			errors.New("Vorschau nicht kodierbar")
	}

	meta := photoMeta{
		ID:         upload.id,
		Name:       upload.name,
		Caption:    caption,
		Uploader:   who,
		UploadedAt: time.Now().UTC().Format(time.RFC3339),
		Width:      img.Bounds().Dx(),
		Height:     img.Bounds().Dy(),
		Bytes:      int64(len(full)),
	}

	metaJSON, err := json.Marshal(meta)

	if err != nil {
		return none, http.StatusInternalServerError,
			errors.New("Metadaten nicht schreibbar")
	}

	// Reihenfolge ist Absicht: Bild, dann Vorschau, dann Metadaten. Die
	// Metadaten machen den Eintrag sichtbar — sie kommen zuletzt, damit die
	// Liste nie auf ein Bild zeigt, das noch nicht vollständig da ist.
	for _, f := range []struct {
		name string
		data []byte
	}{
		{photoFile(upload.id), full},
		{thumbFile(upload.id), thumb},
		{metaFile(upload.id), metaJSON},
	} {
		if err := writeFileAtomically(inDir(f.name), f.data); err != nil {
			log.Printf("goteach-server: %s schreiben: %v", f.name, err)

			return none, http.StatusInternalServerError,
				errors.New("Galerie nicht schreibbar")
		}
	}

	return meta, http.StatusCreated, nil
}

// cleanText macht aus beliebigem Eingabetext eine einzeilige, begrenzte
// Zeichenkette: Steuerzeichen raus, Länge in Zeichen gekappt.
func cleanText(s string, limit int) string {
	var (
		b     strings.Builder
		runes int
	)

	for _, c := range s {
		if runes >= limit {
			break
		}

		if unicode.IsControl(c) {
			c = ' '
		}

		b.WriteRune(c)
		runes++
	}

	return strings.TrimSpace(b.String())
}
