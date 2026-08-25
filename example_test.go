package miotto_test

import (
	"context"
	"fmt"
	"log"
	"os"

	miotto "github.com/cabbagekobe/miotto"
)

// The output is not verified because the example needs the actual device
// (compile check only).
func ExamplePrinter() {
	p, err := miotto.Open()
	if err != nil {
		log.Fatal(err) // powered off, not connected, etc.
	}
	defer p.Close()

	photo, err := os.ReadFile("photo.jpg")
	if err != nil {
		fmt.Println("read failed:", err)
		return
	}
	err = p.Print(context.Background(), photo, miotto.WithProgress(func(pr miotto.Progress) {
		if pr.JobState != "" {
			fmt.Println("job_state:", pr.JobState)
		}
	}))
	if err != nil {
		fmt.Println("print failed:", err)
	}
}
