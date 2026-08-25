package imaging_test

import (
	"encoding/binary"
	"image"
	"testing"

	"github.com/cabbagekobe/miotto/imaging"
)

// Conversion speed for a smartphone-photo-sized input (12MP, shot in portrait
// = stored landscape with Orientation 6).
func BenchmarkPrepare12MPOrientation6(b *testing.B) {
	src := withEXIF(sourceJPEG(b, 4000, 3000, image.Rect(0, 0, 400, 400)), 6, binary.LittleEndian)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := imaging.Prepare(src); err != nil {
			b.Fatal(err)
		}
	}
}
