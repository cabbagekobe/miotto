//go:build hardware

// Smoke test that runs only where the actual device is available:
//
//	go test -tags hardware ./transport/
//
// Requires the printer (powered on, connected via USB). Consumes no paper.
package transport_test

import (
	"testing"
	"time"

	"github.com/cabbagekobe/miotto/transport"
)

func TestOpenWriteReadRoundtrip(t *testing.T) {
	dev, err := transport.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dev.Close()

	// Verify the write→read roundtrip with the debug shell's bat (battery status).
	if err := dev.Write([]byte("bat\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	resp, err := dev.Read(3 * time.Second)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response from device")
	}
	t.Logf("bat: %q", resp)
}
