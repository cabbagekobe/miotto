package session_test

import (
	"fmt"

	"github.com/cabbagekobe/miotto/session"
)

func ExamplePrintSession() {
	s, err := session.New([]byte("jpeg data"), 1)
	if err != nil {
		panic(err)
	}
	// Against the actual device, write each frame from NextRequest to the
	// device and pass the bytes read back to HandleResponse until Finished.
	fmt.Printf("%s\n", s.NextRequest())
	// Output:
	// cmd json
	// {"method":"print_job","params":{"job_type":0,"media_size":5011,"media_type":2010,"copies":1,"channel":2,"file_size":9},"id":1}
}
