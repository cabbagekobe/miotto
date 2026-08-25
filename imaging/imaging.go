// Package imaging converts input images into the 626×939 JPEG the printer accepts.
//
// The outer 6–8px are trimmed off for borderless printing (measured in
// docs/protocol.md). Input is laid over the full area cover-style, with the
// excess cropped around the center.
package imaging

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // PNG input is accepted too

	"golang.org/x/image/draw"
)

// Print resolution (2×3 inch @ 313dpi).
const (
	Width  = 626
	Height = 939
)

// Prepare converts an image file's bytes (JPEG or PNG) into a JPEG for printing.
// It applies the EXIF Orientation and rotates landscape input 90° to portrait.
func Prepare(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cannot decode image: %w", err)
	}

	// A center-anchored cover crop commutes with rotations and flips, so scale
	// down to the output size first and then transform the small image
	// (avoiding per-pixel transforms at full resolution). When the transform
	// swaps the axes, scale to the dimensions that become 626×939 after the swap.
	orientation := orientationOf(data)
	swaps := orientation >= 5 // orientations 5–8 swap the axes
	displayW, displayH := img.Bounds().Dx(), img.Bounds().Dy()
	if swaps {
		displayW, displayH = displayH, displayW
	}
	rotateToPortrait := displayW > displayH
	targetW, targetH := Width, Height
	if swaps != rotateToPortrait {
		targetW, targetH = Height, Width
	}

	fitted := cover(img, targetW, targetH)
	fitted = applyOrientation(fitted, orientation)
	if rotateToPortrait {
		fitted = rotate90CCW(fitted)
	}

	var buf bytes.Buffer
	// Same quality as testchart.jpg, which has a proven print record on the device.
	if err := jpeg.Encode(&buf, fitted, &jpeg.Options{Quality: 92}); err != nil {
		return nil, fmt.Errorf("cannot encode JPEG: %w", err)
	}
	return buf.Bytes(), nil
}

// cover lays src over the full width×height area, cropping the excess around
// the center.
func cover(src image.Image, width, height int) *image.RGBA {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	cw, ch := sw, sh
	if sw*height > width*sh { // horizontal excess
		cw = sh * width / height
	} else { // vertical excess (or exact fit)
		ch = sw * height / width
	}
	x0 := bounds.Min.X + (sw-cw)/2
	y0 := bounds.Min.Y + (sh-ch)/2
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, image.Rect(x0, y0, x0+cw, y0+ch), draw.Src, nil)
	return dst
}

// applyOrientation applies the same 8-way transform as Pillow's exif_transpose.
func applyOrientation(img *image.RGBA, orientation int) *image.RGBA {
	switch orientation {
	case 2:
		return flipH(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipV(img)
	case 5:
		return transpose(img)
	case 6:
		return rotate90CW(img)
	case 7:
		return transverse(img)
	case 8:
		return rotate90CCW(img)
	default:
		return img
	}
}

// remap builds a w×h output, copying each pixel from the src coordinates
// (relative to the bounds origin) returned by at.
func remap(src *image.RGBA, w, h int, at func(x, y int) (sx, sy int)) *image.RGBA {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := dst.Pix[y*dst.Stride : y*dst.Stride+w*4]
		for x := 0; x < w; x++ {
			sx, sy := at(x, y)
			offset := src.PixOffset(bounds.Min.X+sx, bounds.Min.Y+sy)
			copy(row[x*4:x*4+4], src.Pix[offset:offset+4])
		}
	}
	return dst
}

func flipH(img *image.RGBA) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return remap(img, w, h, func(x, y int) (int, int) { return w - 1 - x, y })
}

func flipV(img *image.RGBA) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return remap(img, w, h, func(x, y int) (int, int) { return x, h - 1 - y })
}

func rotate180(img *image.RGBA) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return remap(img, w, h, func(x, y int) (int, int) { return w - 1 - x, h - 1 - y })
}

func rotate90CCW(img *image.RGBA) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return remap(img, h, w, func(x, y int) (int, int) { return w - 1 - y, x })
}

func rotate90CW(img *image.RGBA) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return remap(img, h, w, func(x, y int) (int, int) { return y, h - 1 - x })
}

// transpose flips along the top-left ↔ bottom-right diagonal.
func transpose(img *image.RGBA) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return remap(img, h, w, func(x, y int) (int, int) { return y, x })
}

// transverse flips along the top-right ↔ bottom-left diagonal.
func transverse(img *image.RGBA) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return remap(img, h, w, func(x, y int) (int, int) { return w - 1 - y, h - 1 - x })
}

// orientationOf returns the EXIF Orientation tag, or 1 (as-is) when unreadable.
// The input is an untrusted file, so every read is bounds-checked.
func orientationOf(data []byte) int {
	tiff := exifSegment(data)
	if tiff == nil {
		return 1
	}
	return orientationFromTIFF(tiff)
}

// exifSegment extracts the TIFF portion of the JPEG's APP1 (Exif) segment.
func exifSegment(data []byte) []byte {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 { // SOI
		return nil
	}
	pos := 2
	for pos+2 <= len(data) && data[pos] == 0xFF {
		marker := data[pos+1]
		switch {
		case marker == 0xFF: // padding before a marker
			pos++
			continue
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8): // TEM / RSTn / SOI: no length
			pos += 2
			continue
		case marker == 0xD9 || marker == 0xDA: // APP1 never appears after EOI / SOS
			return nil
		}
		if pos+4 > len(data) {
			return nil
		}
		length := int(binary.BigEndian.Uint16(data[pos+2:])) // includes its own 2 bytes
		if length < 2 || pos+2+length > len(data) {
			return nil
		}
		segment := data[pos+4 : pos+2+length]
		if marker == 0xE1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return segment[6:]
		}
		pos += 2 + length
	}
	return nil
}

// orientationFromTIFF walks IFD0 from the TIFF header and reads Orientation (tag 0x0112).
func orientationFromTIFF(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		order = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:]) != 42 {
		return 1
	}
	ifd := int(order.Uint32(tiff[4:]))
	if ifd < 0 || ifd+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifd:]))
	for i := range count {
		entry := ifd + 2 + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:]) != 0x0112 {
			continue
		}
		if order.Uint16(tiff[entry+2:]) != 3 { // type SHORT
			return 1
		}
		orientation := int(order.Uint16(tiff[entry+8:]))
		if orientation < 1 || orientation > 8 {
			return 1
		}
		return orientation
	}
	return 1
}
