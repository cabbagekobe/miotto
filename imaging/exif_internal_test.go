// Internal tests for the EXIF parser alone, pinning its resilience against
// untrusted input.
package imaging

import (
	"encoding/binary"
	"testing"
)

// exifAPP1 builds an APP1 segment (marker included) carrying only an Orientation tag.
func exifAPP1(orientation uint16, order binary.ByteOrder) []byte {
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
	order.PutUint16(tiff[18:], orientation)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	return append(app1, payload...)
}

func TestOrientationOfSkipsStandaloneMarkersAndPadding(t *testing.T) {
	// Orientation is still found with TEM, RST, and padding FF bytes between
	// the SOI and the APP1.
	data := []byte{0xFF, 0xD8, 0xFF, 0x01, 0xFF, 0xD0, 0xFF}
	data = append(data, exifAPP1(6, binary.LittleEndian)...)
	if got := orientationOf(data); got != 6 {
		t.Errorf("orientationOf = %d, want 6", got)
	}
}

func TestOrientationOfStopsAtSOSAndEOI(t *testing.T) {
	for _, marker := range []byte{0xDA, 0xD9} {
		data := []byte{0xFF, 0xD8, 0xFF, marker, 0x00, 0x04}
		data = append(data, exifAPP1(6, binary.LittleEndian)...)
		if got := orientationOf(data); got != 1 {
			t.Errorf("marker %#x: orientationOf = %d, want 1 (no scan past)", marker, got)
		}
	}
}

func TestOrientationOfRejectsInvalidValues(t *testing.T) {
	cases := map[string][]byte{
		"orientation 0":  append([]byte{0xFF, 0xD8}, exifAPP1(0, binary.LittleEndian)...),
		"orientation 9":  append([]byte{0xFF, 0xD8}, exifAPP1(9, binary.BigEndian)...),
		"not a jpeg":     []byte("not a jpeg"),
		"empty":          nil,
		"soi only":       {0xFF, 0xD8},
		"truncated app1": append([]byte{0xFF, 0xD8}, exifAPP1(6, binary.LittleEndian)[:10]...),
	}
	for name, data := range cases {
		if got := orientationOf(data); got != 1 {
			t.Errorf("%s: orientationOf = %d, want 1", name, got)
		}
	}
}

func FuzzOrientationOf(f *testing.F) {
	f.Add(append([]byte{0xFF, 0xD8}, exifAPP1(6, binary.LittleEndian)...))
	f.Add(append([]byte{0xFF, 0xD8}, exifAPP1(3, binary.BigEndian)...))
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x08, 'E', 'x', 'i', 'f', 0, 0})
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic, and must always return a valid orientation (1–8).
		if got := orientationOf(data); got < 1 || got > 8 {
			t.Errorf("orientationOf = %d, out of range", got)
		}
	})
}
