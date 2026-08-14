// Bildverarbeitung der Galerie: dekodieren, drehen, verkleinern, neu
// kodieren — ausschließlich mit der Standardbibliothek.
//
// Warum überhaupt neu kodiert wird, statt die hochgeladenen Bytes
// abzulegen:
//
//   - Es entfernt EXIF vollständig, inklusive GPS-Koordinaten. Bei Fotos
//     vom Handy ist das der wichtigste Schritt überhaupt — ein Foto vom
//     Spieltag verrät sonst die Wohnadresse dessen, der es gemacht hat.
//   - Es beweist, dass die Datei wirklich ein Bild ist. Was nicht dekodiert,
//     kommt nicht auf die Platte; ein als Foto getarntes SVG oder HTML kann
//     dieser Dienst später nicht als aktiven Inhalt ausliefern.
//   - Die Vorschau fällt dabei sowieso mit ab.
//
// Der Preis: die EXIF-Ausrichtung geht mit verloren. Genau deshalb wird sie
// vorher gelesen und ins Bild hineingerechnet — sonst lägen alle Hochkant-
// Aufnahmen vom Handy in der Galerie auf der Seite.
package server

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // Nur zum Registrieren des PNG-Dekoders für image.Decode.
	"io"
)

// exifScanBytes begrenzt die Suche nach dem EXIF-Block. Der steht in den
// ersten Segmenten einer JPEG-Datei; wer weiter sucht, liest bloß Bilddaten.
const exifScanBytes = 256 << 10

// decodePhoto liest ein Bild und dreht es so, wie die Kamera es gemeint hat.
// Der Rückgabewert trägt keine Metadaten mehr — nur noch Pixel.
func decodePhoto(r io.ReadSeeker) (image.Image, error) {
	head := make([]byte, exifScanBytes)
	n, err := io.ReadFull(r, head)

	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	orient := jpegOrientation(head[:n])

	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	img, _, err := image.Decode(r)

	if err != nil {
		return nil, err
	}

	return applyOrientation(img, orient), nil
}

// jpegOrientation liest das EXIF-Feld Orientation (TIFF-Tag 0x0112) aus dem
// APP1-Segment einer JPEG-Datei. 1 heißt „schon richtig herum" und ist auch
// die Antwort auf alles, was nicht gelesen werden kann: eine falsch geratene
// Drehung wäre schlimmer als keine.
func jpegOrientation(data []byte) int {
	const normal = 1

	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return normal
	}

	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return normal
		}

		marker := data[i+1]

		// Start of Scan: ab hier kommen Bilddaten, kein Segment mehr.
		if marker == 0xDA || marker == 0xD9 {
			return normal
		}

		size := int(binary.BigEndian.Uint16(data[i+2 : i+4]))

		if size < 2 || i+2+size > len(data) {
			return normal
		}

		payload := data[i+4 : i+2+size]

		if marker == 0xE1 && bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
			return tiffOrientation(payload[6:])
		}

		i += 2 + size
	}

	return normal
}

// tiffOrientation durchsucht IFD0 eines TIFF-Headers nach Tag 0x0112.
func tiffOrientation(tiff []byte) int {
	const (
		normal      = 1
		tagOrient   = 0x0112
		entrySize   = 12
		headerSize  = 8
		maxEntries  = 512
		typeShort   = 3
		shortLength = 2
	)

	if len(tiff) < headerSize {
		return normal
	}

	var order binary.ByteOrder

	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		order = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		order = binary.BigEndian
	default:
		return normal
	}

	if order.Uint16(tiff[2:4]) != 0x002A {
		return normal
	}

	offset := int(order.Uint32(tiff[4:8]))

	if offset < headerSize || offset+2 > len(tiff) {
		return normal
	}

	count := int(order.Uint16(tiff[offset : offset+2]))

	if count > maxEntries {
		return normal
	}

	for e := range count {
		at := offset + 2 + e*entrySize

		if at+entrySize > len(tiff) {
			return normal
		}

		if order.Uint16(tiff[at:at+2]) != tagOrient {
			continue
		}

		if order.Uint16(tiff[at+2:at+4]) != typeShort {
			return normal
		}

		// Ein SHORT passt in das Wertfeld und steht dort linksbündig.
		value := int(order.Uint16(tiff[at+8 : at+8+shortLength]))

		if value < 1 || value > 8 {
			return normal
		}

		return value
	}

	return normal
}

// applyOrientation rechnet die EXIF-Ausrichtung ins Bild hinein. Die acht
// Fälle sind die des Standards; 1 (und alles Unbekannte) lässt das Bild in
// Ruhe, und zwar ohne es zu kopieren.
func applyOrientation(src image.Image, orient int) image.Image {
	if orient <= 1 || orient > 8 {
		return src
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h

	// Ab 5 wird die Diagonale getauscht, das Bild also hochkant.
	if orient >= 5 {
		dw, dh = h, w
	}

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))

	for y := range dh {
		for x := range dw {
			var sx, sy int

			switch orient {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}

			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}

	return dst
}

// resizeMax verkleinert auf eine längste Kante von max Pixeln und lässt
// alles Kleinere unangetastet.
//
// Ein Box-Filter (Mittelwert über den Quellbereich jedes Zielpixels) statt
// golang.org/x/image/draw: dieses Repo hat keine einzige externe
// Abhängigkeit, und für „Foto auf Vorschaugröße" lohnt die erste nicht.
// Beim Verkleinern ist der Flächenmittelwert ohnehin das, was man will —
// er mittelt genau die Pixel weg, die sonst als Aliasing zurückblieben.
func resizeMax(src image.Image, edge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	if w <= 0 || h <= 0 || (w <= edge && h <= edge) {
		return src
	}

	dw, dh := edge, edge

	if w >= h {
		dh = h * edge / w
	} else {
		dw = w * edge / h
	}

	dw = max(dw, 1)
	dh = max(dh, 1)

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))

	for y := range dh {
		y0 := b.Min.Y + y*h/dh
		y1 := max(b.Min.Y+(y+1)*h/dh, y0+1)

		for x := range dw {
			x0 := b.Min.X + x*w/dw
			x1 := max(b.Min.X+(x+1)*w/dw, x0+1)

			var sumR, sumG, sumB, n uint64

			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, bl, _ := src.At(sx, sy).RGBA()
					sumR += uint64(r)
					sumG += uint64(g)
					sumB += uint64(bl)
					n++
				}
			}

			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(sumR / n >> 8),
				G: uint8(sumG / n >> 8),
				B: uint8(sumB / n >> 8),
				A: 0xFF,
			})
		}
	}

	return dst
}

// encodeJPEG kodiert ein Bild als JPEG — die einzige Form, in der die
// Galerie ablegt und ausliefert. Was hier herauskommt, trägt keine
// Metadaten: Go schreibt weder EXIF noch XMP.
func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer

	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
