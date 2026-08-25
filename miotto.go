package miotto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/cabbagekobe/miotto/imaging"
	"github.com/cabbagekobe/miotto/protocol"
	"github.com/cabbagekobe/miotto/session"
	"github.com/cabbagekobe/miotto/transport"
)

// ErrCoverOpen indicates the preflight check found the printer cover open.
var ErrCoverOpen = errors.New("printer cover is open")

// ackTimeout is the wait for a chunk ack; rpcTimeout is the wait for a
// JSON-RPC reply (measured values plus headroom).
const (
	ackTimeout          = 2 * time.Second
	rpcTimeout          = 4 * time.Second
	defaultPollInterval = 2 * time.Second

	// maxDrainReads caps the read loops used for draining and the preflight
	// check, guaranteeing they terminate even when the device streams
	// continuously (log start and the like).
	maxDrainReads = 32
)

// Device is the USB surface Printer needs. transport.Device satisfies it.
// Tests and custom transports can hand their own implementation to NewPrinter.
type Device interface {
	Write(frame []byte) error
	Read(timeout time.Duration) ([]byte, error)
	Close()
}

// Progress is a snapshot of print progress. During chunk transfer
// ChunksSent/ChunksTotal advance; during job_info polling JobState is set
// (it is empty while transferring). When JobState is
// session.JobStatePending the printer is paused (out of paper etc.) and the
// job resumes automatically once paper is loaded.
type Progress struct {
	JobState    string
	ChunksSent  int
	ChunksTotal int
}

// PrintOption adjusts the behavior of Print.
type PrintOption func(*printConfig)

type printConfig struct {
	copies       int
	pollInterval time.Duration
	progress     func(Progress)
}

// WithCopies sets the number of copies to print (default 1).
func WithCopies(n int) PrintOption {
	return func(c *printConfig) { c.copies = n }
}

// WithProgress sets a callback that receives progress notifications.
// The callback is invoked on the same goroutine as Print.
func WithProgress(fn func(Progress)) PrintOption {
	return func(c *printConfig) { c.progress = fn }
}

// WithPollInterval sets the job_info polling interval (default 2 seconds;
// zero or negative means no wait).
func WithPollInterval(d time.Duration) PrintOption {
	return func(c *printConfig) { c.pollInterval = d }
}

// Printer is the high-level API for a connected printer. It is meant to be
// used from a single goroutine and does not guard against concurrent access.
type Printer struct {
	dev Device
}

// Open finds and connects to the printer. The device powers itself off after
// a few minutes, so when it cannot be found Open returns
// transport.ErrNotFound (turn the printer on and retry).
func Open() (*Printer, error) {
	dev, err := transport.Open()
	if err != nil {
		return nil, err
	}
	return NewPrinter(dev), nil
}

// NewPrinter builds a Printer on any Device. Normally use Open instead.
func NewPrinter(dev Device) *Printer {
	return &Printer{dev: dev}
}

// Close releases the connection.
func (p *Printer) Close() {
	p.dev.Close()
}

// Print prints an image (JPEG or PNG bytes) as one job and waits until
// job_state reaches finished. Applying the EXIF orientation and the cover
// resize to 626×939 happen automatically. Cancel ctx to abort (a long
// pending wait for paper is for the caller to bound via ctx).
func (p *Printer) Print(ctx context.Context, imageData []byte, opts ...PrintOption) error {
	cfg := printConfig{copies: 1, pollInterval: defaultPollInterval}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := ctx.Err(); err != nil {
		return err // honor cancellation before the conversion and preflight USB I/O
	}

	jpeg, err := imaging.Prepare(imageData)
	if err != nil {
		return err
	}
	s, err := session.New(jpeg, cfg.copies)
	if err != nil {
		return err
	}

	p.drain()
	if err := p.checkCover(); err != nil {
		return err
	}

	chunksTotal := len(protocol.Chunks(jpeg))
	chunksSent := 0
	for frame := s.NextRequest(); frame != nil; frame = s.NextRequest() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.dev.Write(frame); err != nil {
			return fmt.Errorf("usb write: %w", err)
		}
		timeout := rpcTimeout
		isData := bytes.HasPrefix(frame, []byte("cmd data"))
		if isData {
			timeout = ackTimeout
		}
		resp, err := p.dev.Read(timeout)
		if err != nil {
			return fmt.Errorf("usb read: %w", err)
		}
		if err := s.HandleResponse(resp); err != nil {
			return err
		}
		if isData {
			chunksSent++
		}
		if cfg.progress != nil && (isData || s.JobState() != "") {
			cfg.progress(Progress{
				JobState:    s.JobState(),
				ChunksSent:  chunksSent,
				ChunksTotal: chunksTotal,
			})
		}
		if s.JobState() != "" && !s.Finished() {
			if err := sleepCtx(ctx, cfg.pollInterval); err != nil {
				return err
			}
		}
	}
	return nil
}

// Shell sends one command to the firmware's debug shell and returns the raw
// reply (empty when the device stays silent — some commands answer on UART
// only). See docs/protocol.md for the known commands.
//
// Warning: producttest, i2c, and otp have unpredictable side effects and
// should never be sent.
func (p *Printer) Shell(command string) ([]byte, error) {
	return p.shellExec(command)
}

// shellExec sends a debug-shell command and reads the reply with the bounded
// pattern shared by Shell, Status, and checkCover.
func (p *Printer) shellExec(command string) ([]byte, error) {
	if err := p.dev.Write([]byte(command + "\n")); err != nil {
		return nil, err
	}
	var out []byte
	wait := 2 * time.Second // wait for the first reply, then drain the rest
	for range maxDrainReads {
		b, err := p.dev.Read(wait)
		if err != nil {
			break
		}
		out = append(out, b...)
		wait = 400 * time.Millisecond
	}
	return out, nil
}

// Status is a snapshot of the printer's state, read via the debug shell
// (apd and bat; the JSON-RPC get_prop vocabulary is unknown, see
// docs/protocol.md).
type Status struct {
	BatteryPercent int
	CoverOpen      bool
}

// Status reads the printer's battery level and cover state. Unlike the
// preflight check in Print (which fails open), Status returns an error when
// the sensors cannot be read.
func (p *Printer) Status() (Status, error) {
	p.drain()
	apd, err := p.shellExec("apd")
	if err != nil {
		return Status{}, fmt.Errorf("usb write: %w", err)
	}
	if !bytes.Contains(apd, []byte("cover=")) {
		return Status{}, fmt.Errorf("cannot read sensors (apd): %q", apd)
	}
	soc, err := p.shellExec("bat soc")
	if err != nil {
		return Status{}, fmt.Errorf("usb write: %w", err)
	}
	percent, err := parseBatteryPercent(soc)
	if err != nil {
		return Status{}, err
	}
	return Status{
		BatteryPercent: percent,
		CoverOpen:      bytes.Contains(apd, []byte("cover=0")),
	}, nil
}

var batteryNumber = regexp.MustCompile(`\d+`)

// parseBatteryPercent extracts the percentage from a `bat soc` reply.
func parseBatteryPercent(reply []byte) (int, error) {
	m := batteryNumber.Find(reply)
	if m == nil {
		return 0, fmt.Errorf("cannot parse battery level: %q", reply)
	}
	n, err := strconv.Atoi(string(m))
	if err != nil || n > 100 {
		return 0, fmt.Errorf("battery level out of range: %q", reply)
	}
	return n, nil
}

// checkCover asks the debug shell (apd) whether the cover is open before
// printing. When the reply is unreadable or carries no cover= field, printing
// proceeds (fail-open).
func (p *Printer) checkCover() error {
	out, err := p.shellExec("apd")
	if err != nil {
		return nil
	}
	if bytes.Contains(out, []byte("cover=0")) {
		return ErrCoverOpen
	}
	return nil
}

// drain discards any buffered incoming data (events left over from before the
// connection).
func (p *Printer) drain() {
	for range maxDrainReads {
		if _, err := p.dev.Read(200 * time.Millisecond); err != nil {
			return
		}
	}
}

// sleepCtx waits for d, returning early when ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
