package editor

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// exifJPEG builds a JPEG whose APP1 segment carries only an Orientation
// tag, so orientation parsing can be tested without a real camera file.
func exifJPEG(t *testing.T, img image.Image, orientation uint16) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := jpeg.Encode(&body, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	raw := body.Bytes() // FF D8 ... ; insert APP1 after SOI
	var tiff bytes.Buffer
	tiff.Write([]byte{'I', 'I', 0x2A, 0x00})                 // little-endian TIFF header
	binary.Write(&tiff, binary.LittleEndian, uint32(8))      //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, uint16(1))      //nolint:errcheck // one IFD entry
	binary.Write(&tiff, binary.LittleEndian, uint16(0x0112)) //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, uint16(3))      //nolint:errcheck // SHORT
	binary.Write(&tiff, binary.LittleEndian, uint32(1))      //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, orientation)    //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, uint16(0))      //nolint:errcheck // pad value to 4 bytes
	binary.Write(&tiff, binary.LittleEndian, uint32(0))      //nolint:errcheck // next IFD
	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write(raw[:2])
	out.Write([]byte{0xFF, 0xE1})
	binary.Write(&out, binary.BigEndian, uint16(len(payload)+2)) //nolint:errcheck
	out.Write(payload)
	out.Write(raw[2:])
	return out.Bytes()
}

// landscape: 40x20, left half red, right half blue.
func landscape() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 20 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func TestOrientationParsed(t *testing.T) {
	data := exifJPEG(t, landscape(), 6)
	if got := orientation(bytes.NewReader(data)); got != 6 {
		t.Fatalf("orientation=%d want 6", got)
	}
	var plain bytes.Buffer
	_ = jpeg.Encode(&plain, landscape(), nil)
	if got := orientation(bytes.NewReader(plain.Bytes())); got != 1 {
		t.Fatalf("plain orientation=%d want 1", got)
	}
}

func isRed(c color.Color) bool  { r, _, b, _ := c.RGBA(); return r > 0x8000 && b < 0x8000 }
func isBlue(c color.Color) bool { r, _, b, _ := c.RGBA(); return b > 0x8000 && r < 0x8000 }

func TestThumbnailAppliesOrientationAndFits(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rot.jpg")
	if err := os.WriteFile(p, exifJPEG(t, landscape(), 6), 0o644); err != nil {
		t.Fatal(err)
	}
	img, err := thumbnail(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	// Orientation 6 = rotate 90° clockwise: 40x20 becomes 20x40, fit in 10 → 5x10.
	if b.Dx() != 5 || b.Dy() != 10 {
		t.Fatalf("bounds %v", b)
	}
	// After clockwise rotation the red (left) half is on top.
	if !isRed(img.At(2, 1)) || !isBlue(img.At(2, 8)) {
		t.Fatalf("top=%v bottom=%v", img.At(2, 1), img.At(2, 8))
	}
}

func TestThumbnailNoRotationKeepsAspect(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plain.jpg")
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, landscape(), nil)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	img, err := thumbnail(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 10 || b.Dy() != 5 {
		t.Fatalf("bounds %v", b)
	}
}
