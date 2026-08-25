package imaging_test

import (
	"bytes"
	"fmt"
	"image"
	"os"

	"github.com/cabbagekobe/miotto/imaging"
)

func ExamplePrepare() {
	data, err := os.ReadFile("testdata/orientation3.jpg")
	if err != nil {
		panic(err)
	}
	out, err := imaging.Prepare(data)
	if err != nil {
		panic(err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		panic(err)
	}
	fmt.Printf("%dx%d\n", img.Bounds().Dx(), img.Bounds().Dy())
	// Output:
	// 626x939
}
