// Verifies Printer (the high-level API) against a fake device.
// The fake is request/response shaped — each Write makes one response
// readable — so draining (reading without a pending write) errors like a
// timeout would.
package miotto_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"testing"
	"time"

	miotto "github.com/cabbagekobe/miotto"
	"github.com/cabbagekobe/miotto/session"
)

// sourceJPEG builds a solid-color JPEG (a real image so Prepare succeeds).
func sourceJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const coverClosed = "senA=0 senB=0 cover=1\nOK\n"

type fakeDevice struct {
	responses [][]byte
	writes    [][]byte
	pending   int // each Write makes one response readable
	writeErr  error
	readErr   error
	closed    bool
}

func (f *fakeDevice) Write(frame []byte) error {
	f.writes = append(f.writes, bytes.Clone(frame))
	if f.writeErr != nil {
		return f.writeErr
	}
	f.pending++
	return nil
}

func (f *fakeDevice) Read(_ time.Duration) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.pending == 0 || len(f.responses) == 0 {
		return nil, errors.New("timeout")
	}
	f.pending--
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (f *fakeDevice) Close() { f.closed = true }

// testImage is a small real image that goes through imaging.Prepare.
func testImage(t *testing.T) []byte {
	t.Helper()
	return sourceJPEG(t, 100, 150)
}

// happyFlow builds the response script: apd (cover closed) → print_job →
// acks for every chunk → job_info replies for the given states.
func happyFlow(t *testing.T, chunks int, states ...string) [][]byte {
	t.Helper()
	responses := [][]byte{[]byte(coverClosed), []byte(`{"result":{"job_id":92},"id":1}`)}
	for range chunks {
		responses = append(responses, []byte("cmd data EXTLEN=0 OK\n"))
	}
	for i, state := range states {
		responses = append(responses,
			fmt.Appendf(nil, `{"result":[{"job_id":92,"job_state":"%s"}],"id":%d}`, state, 2+i))
	}
	return responses
}

func TestPrintCompletesFlow(t *testing.T) {
	dev := &fakeDevice{responses: happyFlow(t, chunkCount(t), "printing", "finished")}
	p := miotto.NewPrinter(dev)
	var progress []miotto.Progress
	err := p.Print(context.Background(), testImage(t),
		miotto.WithPollInterval(0),
		miotto.WithProgress(func(pr miotto.Progress) { progress = append(progress, pr) }))
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) == 0 {
		t.Fatal("progress should be reported")
	}
	last := progress[len(progress)-1]
	if last.JobState != "finished" {
		t.Errorf("last progress = %+v, want finished", last)
	}
	if first := progress[0]; first.ChunksSent != 1 || first.ChunksTotal < 1 {
		t.Errorf("first progress = %+v, want chunk 1/n", first)
	}
}

// chunkCount measures how many data frames testImage produces (to size the
// fake's response script).
func chunkCount(t *testing.T) int {
	t.Helper()
	dev := &fakeDevice{responses: happyFlow(t, 64, "finished")} // over-provision acks, then count
	p := miotto.NewPrinter(dev)
	_ = p.Print(context.Background(), testImage(t), miotto.WithPollInterval(0))
	n := 0
	for _, w := range dev.writes {
		if bytes.HasPrefix(w, []byte("cmd data")) {
			n++
		}
	}
	if n == 0 {
		t.Fatal("no data frames written")
	}
	return n
}

func TestPrintForwardsCopies(t *testing.T) {
	dev := &fakeDevice{responses: happyFlow(t, chunkCount(t), "finished")}
	p := miotto.NewPrinter(dev)
	if err := p.Print(context.Background(), testImage(t),
		miotto.WithCopies(3), miotto.WithPollInterval(0)); err != nil {
		t.Fatal(err)
	}
	// writes[0] is the apd, writes[1] is the print_job.
	if !bytes.Contains(dev.writes[1], []byte(`"copies":3`)) {
		t.Errorf("print_job frame should contain copies=3: %q", dev.writes[1])
	}
}

func TestPrintReportsPendingAndResumes(t *testing.T) {
	dev := &fakeDevice{responses: happyFlow(t, chunkCount(t),
		"printing", "pending", "pending", "printing", "finished")}
	p := miotto.NewPrinter(dev)
	pendings := 0
	err := p.Print(context.Background(), testImage(t),
		miotto.WithPollInterval(0),
		miotto.WithProgress(func(pr miotto.Progress) {
			if pr.JobState == session.JobStatePending {
				pendings++
			}
		}))
	if err != nil {
		t.Fatal(err)
	}
	if pendings != 2 {
		t.Errorf("pending progress = %d, want 2", pendings)
	}
}

func TestPrintStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dev := &fakeDevice{responses: happyFlow(t, chunkCount(t), "finished")}
	p := miotto.NewPrinter(dev)
	err := p.Print(ctx, testImage(t), miotto.WithPollInterval(0))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	// Once cancelled, nothing is written to USB — not even the preflight.
	if len(dev.writes) != 0 {
		t.Errorf("writes = %d, want 0", len(dev.writes))
	}
}

func TestPrintBlocksWhenCoverOpen(t *testing.T) {
	dev := &fakeDevice{responses: [][]byte{[]byte("senA=0 senB=0 cover=0\nOK\n")}}
	p := miotto.NewPrinter(dev)
	err := p.Print(context.Background(), testImage(t), miotto.WithPollInterval(0))
	if !errors.Is(err, miotto.ErrCoverOpen) {
		t.Fatalf("want ErrCoverOpen, got %v", err)
	}
	// No print frames were sent (writes hold the apd only).
	if len(dev.writes) != 1 {
		t.Errorf("writes = %d, want 1 (apd only)", len(dev.writes))
	}
}

func TestPrintContinuesWhenCoverCheckUnreadable(t *testing.T) {
	// Printing proceeds even when the apd reply has no cover= field (fail-open).
	responses := happyFlow(t, chunkCount(t), "finished")
	responses[0] = []byte("unrelated shell output\n")
	dev := &fakeDevice{responses: responses}
	p := miotto.NewPrinter(dev)
	if err := p.Print(context.Background(), testImage(t), miotto.WithPollInterval(0)); err != nil {
		t.Fatal(err)
	}
}

func TestPrintPropagatesRPCError(t *testing.T) {
	dev := &fakeDevice{responses: [][]byte{
		[]byte(coverClosed),
		[]byte(`{"method":"event.rpt_err","params":{"code":-8201,"message":"json parse error"}}`),
	}}
	p := miotto.NewPrinter(dev)
	err := p.Print(context.Background(), testImage(t), miotto.WithPollInterval(0))
	var rpcErr *session.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -8201 {
		t.Fatalf("want RPCError -8201, got %v", err)
	}
}

func TestPrintPropagatesWriteError(t *testing.T) {
	dev := &fakeDevice{writeErr: errors.New("usb gone")}
	p := miotto.NewPrinter(dev)
	if err := p.Print(context.Background(), testImage(t), miotto.WithPollInterval(0)); err == nil {
		t.Fatal("want error when write fails")
	}
}

func TestPrintPropagatesReadError(t *testing.T) {
	// Every read fails: preflight is skipped fail-open, then the print_job
	// reply wait fails.
	dev := &fakeDevice{readErr: errors.New("usb timeout")}
	p := miotto.NewPrinter(dev)
	if err := p.Print(context.Background(), testImage(t), miotto.WithPollInterval(0)); err == nil {
		t.Fatal("want error when read fails")
	}
}

func TestPrintRejectsInvalidCopies(t *testing.T) {
	dev := &fakeDevice{}
	p := miotto.NewPrinter(dev)
	if err := p.Print(context.Background(), testImage(t),
		miotto.WithCopies(0), miotto.WithPollInterval(0)); err == nil {
		t.Fatal("want error for copies=0")
	}
}

// endlessDevice returns data forever (mimicking a streaming state such as log start).
type endlessDevice struct {
	reads int
}

func (e *endlessDevice) Write(_ []byte) error { return nil }

func (e *endlessDevice) Read(_ time.Duration) ([]byte, error) {
	e.reads++
	return []byte("stream data\n"), nil
}

func (e *endlessDevice) Close() {}

func TestPrintTerminatesOnEndlessStream(t *testing.T) {
	// The drain/preflight reads are bounded, and the flow stops with an error
	// (no hang).
	dev := &endlessDevice{}
	p := miotto.NewPrinter(dev)
	if err := p.Print(context.Background(), testImage(t), miotto.WithPollInterval(0)); err == nil {
		t.Fatal("want error on stream garbage")
	}
	if dev.reads > 200 {
		t.Errorf("reads = %d, unbounded read loop suspected", dev.reads)
	}
}

func TestCloseClosesDevice(t *testing.T) {
	dev := &fakeDevice{}
	p := miotto.NewPrinter(dev)
	p.Close()
	if !dev.closed {
		t.Error("Close should close the device")
	}
}
