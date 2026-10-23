package editor

import (
	"encoding/binary"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"

	"golang.org/x/image/draw"
)

// thumbnail decodes the image at path, applies its EXIF orientation and
// scales it to fit in a size×size box.
func thumbnail(path string, size int) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only file
	o := orientation(f)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > h {
		h = max(1, h*size/w)
		w = size
	} else {
		w = max(1, w*size/h)
		h = size
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	// Orient the small image, not the multi-megapixel source: the
	// per-pixel transform is cheap at thumbnail size and the fit box is
	// square, so rotating afterwards still fits.
	return orient(dst, o), nil
}

// orient returns img transformed per EXIF orientation o (1..8).
func orient(img image.Image, o int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	var at func(x, y int) (int, int) // dst coords → src coords
	switch o {
	case 2:
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
		at = func(x, y int) (int, int) { return w - 1 - x, y }
	case 3:
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
		at = func(x, y int) (int, int) { return w - 1 - x, h - 1 - y }
	case 4:
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
		at = func(x, y int) (int, int) { return x, h - 1 - y }
	case 5:
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		at = func(x, y int) (int, int) { return y, x }
	case 6: // rotate 90° clockwise
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		at = func(x, y int) (int, int) { return y, h - 1 - x }
	case 7:
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		at = func(x, y int) (int, int) { return w - 1 - y, h - 1 - x }
	case 8: // rotate 90° anticlockwise
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		at = func(x, y int) (int, int) { return w - 1 - y, x }
	default:
		return img
	}
	db := dst.Bounds()
	for y := 0; y < db.Dy(); y++ {
		for x := 0; x < db.Dx(); x++ {
			sx, sy := at(x, y)
			dst.Set(x, y, img.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

// orientation reads the EXIF Orientation tag (1..8) from a JPEG stream.
// Anything unreadable yields 1 (as stored).
func orientation(r io.Reader) int {
	var soi [2]byte
	if _, err := io.ReadFull(r, soi[:]); err != nil || soi != [2]byte{0xFF, 0xD8} {
		return 1
	}
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil || hdr[0] != 0xFF {
			return 1
		}
		marker := hdr[1]
		length := int(binary.BigEndian.Uint16(hdr[2:])) - 2
		if length < 0 {
			return 1
		}
		if marker == 0xDA || marker == 0xD9 { // start of scan / end: no EXIF ahead
			return 1
		}
		if marker != 0xE1 {
			if _, err := io.CopyN(io.Discard, r, int64(length)); err != nil {
				return 1
			}
			continue
		}
		seg := make([]byte, length)
		if _, err := io.ReadFull(r, seg); err != nil {
			return 1
		}
		if len(seg) < 6 || string(seg[:6]) != "Exif\x00\x00" {
			continue
		}
		return tiffOrientation(seg[6:])
	}
}

// tiffOrientation finds tag 0x0112 in IFD0 of a TIFF blob.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	ifd := int(bo.Uint32(t[4:8]))
	if ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd : ifd+2]))
	for i := range n {
		e := ifd + 2 + i*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:e+2]) == 0x0112 && bo.Uint16(t[e+2:e+4]) == 3 {
			v := int(bo.Uint16(t[e+8 : e+10]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
