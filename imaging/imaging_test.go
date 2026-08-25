// Input image → 626×939 JPEG conversion.
// Pixel assertions sample points away from edges to avoid JPEG compression artifacts.
package imaging_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"testing"

	"github.com/cabbagekobe/miotto/imaging"
)

// sourceJPEG builds a black JPEG with the given rectangles painted white.
func sourceJPEG(t testing.TB, width, height int, whiteRects ...image.Rectangle) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	for _, r := range whiteRects {
		draw.Draw(img, r, image.NewUniform(color.White), image.Point{}, draw.Src)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" {
		t.Fatalf("output format = %q, want jpeg", format)
	}
	return img
}

func brightness(img image.Image, x, y int) uint8 {
	r, _, _, _ := img.At(x, y).RGBA()
	return uint8(r >> 8)
}

func assertSize(t *testing.T, img image.Image) {
	t.Helper()
	if img.Bounds().Dx() != imaging.Width || img.Bounds().Dy() != imaging.Height {
		t.Fatalf("output size = %dx%d, want %dx%d",
			img.Bounds().Dx(), img.Bounds().Dy(), imaging.Width, imaging.Height)
	}
}

func TestOutputIsJPEGOfPrintSize(t *testing.T) {
	out, err := imaging.Prepare(sourceJPEG(t, 1000, 1500))
	if err != nil {
		t.Fatal(err)
	}
	assertSize(t, decode(t, out))
}

func TestSmallInputIsUpscaledToPrintSize(t *testing.T) {
	out, err := imaging.Prepare(sourceJPEG(t, 100, 150))
	if err != nil {
		t.Fatal(err)
	}
	assertSize(t, decode(t, out))
}

func TestSquareInputIsCroppedAtCenter(t *testing.T) {
	// A square with a black left half and a white right half. A cover crop
	// trims left and right evenly, so the boundary stays at the center (x=313).
	out, err := imaging.Prepare(sourceJPEG(t, 100, 100, image.Rect(50, 0, 100, 100)))
	if err != nil {
		t.Fatal(err)
	}
	decoded := decode(t, out)
	centerY := imaging.Height / 2
	if b := brightness(decoded, 280, centerY); b > 50 {
		t.Errorf("left of the boundary should be black, brightness = %d", b)
	}
	if b := brightness(decoded, 346, centerY); b < 200 {
		t.Errorf("right of the boundary should be white, brightness = %d", b)
	}
	// A left-anchored crop would put the boundary at x=469, making both points black.
}

func TestLandscapeInputIsRotatedToPortrait(t *testing.T) {
	// Landscape input is rotated 90° (counter-clockwise): the right (white)
	// half of the source ends up on top.
	out, err := imaging.Prepare(sourceJPEG(t, 200, 100, image.Rect(100, 0, 200, 100)))
	if err != nil {
		t.Fatal(err)
	}
	decoded := decode(t, out)
	assertSize(t, decoded)
	centerX := imaging.Width / 2
	if b := brightness(decoded, centerX, 10); b < 200 {
		t.Errorf("top should be white, brightness = %d", b)
	}
	if b := brightness(decoded, centerX, imaging.Height-10); b > 50 {
		t.Errorf("bottom should be black, brightness = %d", b)
	}
}

// spliceAPP1 inserts an APP1 segment right after the SOI of jpegData.
func spliceAPP1(jpegData, payload []byte) []byte {
	app1 := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	app1 = append(app1, payload...)
	out := append([]byte{0xFF, 0xD8}, app1...)
	return append(out, jpegData[2:]...)
}

// withEXIF inserts an EXIF APP1 carrying only an Orientation tag into jpegData.
func withEXIF(jpegData []byte, orientation int, order binary.ByteOrder) []byte {
	tiff := make([]byte, 26)
	if order == binary.ByteOrder(binary.LittleEndian) {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8)       // IFD0 offset
	order.PutUint16(tiff[8:], 1)       // entry count
	order.PutUint16(tiff[10:], 0x0112) // Orientation
	order.PutUint16(tiff[12:], 3)      // type SHORT
	order.PutUint32(tiff[14:], 1)      // value count
	order.PutUint16(tiff[18:], uint16(orientation))
	// tiff[20:22] (rest of the value field) and tiff[22:26] (next IFD offset) stay zero.
	return spliceAPP1(jpegData, append([]byte("Exif\x00\x00"), tiff...))
}

func TestAllEXIFOrientationsAreApplied(t *testing.T) {
	type corner struct{ x, y int }
	tl := corner{30, 30}
	tr := corner{imaging.Width - 31, 30}
	bl := corner{30, imaging.Height - 31}
	br := corner{imaging.Width - 31, imaging.Height - 31}
	// Which displayed corner the white marker at the stored image's top-left
	// 40×40 ends up in. Orientations 5–8 swap the axes, so their stored image
	// is landscape to keep the displayed image portrait.
	cases := []struct {
		orientation int
		w, h        int
		white       corner
	}{
		{1, 100, 150, tl}, {2, 100, 150, tr}, {3, 100, 150, br}, {4, 100, 150, bl},
		{5, 150, 100, tl}, {6, 150, 100, tr}, {7, 150, 100, br}, {8, 150, 100, bl},
	}
	for _, tc := range cases {
		// Even orientations are written big-endian (MM) to cover both byte orders.
		order := binary.ByteOrder(binary.LittleEndian)
		if tc.orientation%2 == 0 {
			order = binary.BigEndian
		}
		src := withEXIF(sourceJPEG(t, tc.w, tc.h, image.Rect(0, 0, 40, 40)), tc.orientation, order)
		out, err := imaging.Prepare(src)
		if err != nil {
			t.Fatalf("orientation %d: %v", tc.orientation, err)
		}
		decoded := decode(t, out)
		assertSize(t, decoded)
		if b := brightness(decoded, tc.white.x, tc.white.y); b < 200 {
			t.Errorf("orientation %d: marker corner should be white, brightness = %d", tc.orientation, b)
		}
		opposite := corner{imaging.Width - 1 - tc.white.x, imaging.Height - 1 - tc.white.y}
		if b := brightness(decoded, opposite.x, opposite.y); b > 50 {
			t.Errorf("orientation %d: opposite corner should be black, brightness = %d", tc.orientation, b)
		}
	}
}

func TestMalformedEXIFIsIgnored(t *testing.T) {
	// Broken EXIF is treated as "no orientation info"; the conversion itself succeeds.
	src := spliceAPP1(sourceJPEG(t, 100, 150, image.Rect(0, 0, 40, 40)), []byte("Exif\x00\x00garbage"))
	out, err := imaging.Prepare(src)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decode(t, out)
	assertSize(t, decoded)
	if b := brightness(decoded, 30, 30); b < 200 {
		t.Errorf("marker should stay at top-left, brightness = %d", b)
	}
}

func TestEXIFOrientationIsApplied(t *testing.T) {
	// Orientation=3 (display rotated 180°): smartphone photos must come out
	// the right way up.
	// testdata: 100×150, black top half / white bottom half → after the 180°
	// rotation the top is white.
	data, err := os.ReadFile("testdata/orientation3.jpg")
	if err != nil {
		t.Fatal(err)
	}
	out, err := imaging.Prepare(data)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decode(t, out)
	centerX := imaging.Width / 2
	if b := brightness(decoded, centerX, 10); b < 200 {
		t.Errorf("top should be white after 180° rotation, brightness = %d", b)
	}
	if b := brightness(decoded, centerX, imaging.Height-10); b > 50 {
		t.Errorf("bottom should be black after 180° rotation, brightness = %d", b)
	}
}
