// Package session is the print-flow state machine. It only returns "the next
// bytes to send" and performs no I/O.
//
// The caller writes the frame returned by NextRequest to the device, then
// passes the bytes read back to HandleResponse, repeatedly. Controlling the
// polling interval (sleeping) is also the caller's responsibility.
package session

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cabbagekobe/miotto/protocol"
)

// ErrAborted indicates the job became aborted (a data-transfer failure or an
// interruption on the printer side).
var ErrAborted = errors.New("job aborted (data transfer failure or interruption on the printer side)")

// JobStatePending is the paused state caused by running out of paper, a feed
// failure, and the like. It is not an error: the job resumes automatically
// once paper is loaded (measured on the actual device; no error event is
// reported). The session keeps polling in this state.
const JobStatePending = "pending"

// RPCError is an error response returned by the device.
type RPCError = protocol.RPCError

type state int

const (
	stateCreate state = iota
	stateTransfer
	stateMonitor
	stateFinished
	stateFailed
)

// Vocabulary of print_job parameters (docs/protocol.md). Only values whose
// meaning was confirmed by measurement are named.
const (
	mediaSize2x3Inch    = 5011
	mediaTypePhotoPaper = 2010
)

// The field order of printJobParams is the JSON key order proven on the actual
// device. Do not change it.
type printJobParams struct {
	JobType   int `json:"job_type"`
	MediaSize int `json:"media_size"`
	MediaType int `json:"media_type"`
	Copies    int `json:"copies"`
	Channel   int `json:"channel"`
	FileSize  int `json:"file_size"`
}

// PrintSession is the state machine that advances the job flow of printing
// one JPEG.
type PrintSession struct {
	chunks     [][]byte
	fileSize   int
	copies     int
	rpcID      int // last RPC id whose response was processed; the next to send is rpcID+1
	chunkIndex int
	state      state
	jobID      int
	jobState   string
}

// New creates a session that prints jpeg in the given number of copies.
func New(jpeg []byte, copies int) (*PrintSession, error) {
	if copies < 1 {
		return nil, fmt.Errorf("copies must be at least 1: %d", copies)
	}
	if len(jpeg) == 0 {
		return nil, errors.New("empty jpeg data")
	}
	return &PrintSession{
		chunks:   protocol.Chunks(jpeg),
		fileSize: len(jpeg),
		copies:   copies,
	}, nil
}

// Finished reports whether printing reached the finished state.
func (s *PrintSession) Finished() bool {
	return s.state == stateFinished
}

// JobState returns the last job_state reported by the device ("waiting" etc.).
func (s *PrintSession) JobState() string {
	return s.jobState
}

// NextRequest returns the next frame to write to the device, or nil when the
// flow has ended. It does not mutate state, so it returns the same frame
// until a response is passed to HandleResponse.
func (s *PrintSession) NextRequest() []byte {
	switch s.state {
	case stateCreate:
		// job_type 0 / channel 2 are values proven on the actual device
		// (meaning not yet determined; see protocol.md).
		return protocol.FrameJSON("print_job", printJobParams{
			JobType:   0,
			MediaSize: mediaSize2x3Inch,
			MediaType: mediaTypePhotoPaper,
			Copies:    s.copies,
			Channel:   2,
			FileSize:  s.fileSize,
		}, s.rpcID+1)
	case stateTransfer:
		return protocol.FrameData(uint32(s.jobID), s.chunks[s.chunkIndex])
	case stateMonitor:
		return protocol.FrameJSON("job_info", []int{s.jobID}, s.rpcID+1)
	default:
		return nil
	}
}

// HandleResponse feeds a response read from the device into the state machine.
// When it returns an error the session has ended, and NextRequest returns nil
// from then on.
func (s *PrintSession) HandleResponse(raw []byte) error {
	if s.state == stateTransfer {
		if protocol.IsDataAck(raw) {
			s.chunkIndex++
			if s.chunkIndex == len(s.chunks) {
				s.state = stateMonitor
			}
			return nil
		}
		// Not an ack — but if it is a device error response, return it typed.
		if resp := protocol.ParseJSONResponse(raw); resp != nil {
			if rpcErr := protocol.ErrorOf(resp); rpcErr != nil {
				return s.fail(rpcErr)
			}
		}
		return s.fail(fmt.Errorf("chunk was not accepted: %q", raw))
	}

	resp := protocol.ParseJSONResponse(raw)
	if resp == nil {
		return s.fail(fmt.Errorf("cannot parse response: %q", raw))
	}
	if rpcErr := protocol.ErrorOf(resp); rpcErr != nil {
		return s.fail(rpcErr)
	}

	switch s.state {
	case stateCreate:
		// Resending print_job would create another job, so an unexpected
		// response here is a failure, not something to ignore.
		if resp.ID == nil || *resp.ID != s.rpcID+1 {
			return s.fail(fmt.Errorf("not a response to print_job: %q", raw))
		}
		var result struct {
			JobID *int `json:"job_id"`
		}
		if resp.Result == nil || json.Unmarshal(resp.Result, &result) != nil || result.JobID == nil {
			return s.fail(fmt.Errorf("print_job response has no job_id: %q", raw))
		}
		s.rpcID++
		s.jobID = *result.JobID
		s.state = stateTransfer
	case stateMonitor:
		// Re-polling job_info is idempotent, so in this state only, ignore
		// unexpected JSON and continue (events other than rpt_err, and
		// responses whose id does not match).
		if resp.Method != "" {
			return nil
		}
		if resp.ID == nil || *resp.ID != s.rpcID+1 {
			return nil
		}
		var infos []struct {
			JobState string `json:"job_state"`
		}
		if resp.Result == nil || json.Unmarshal(resp.Result, &infos) != nil || len(infos) == 0 {
			return s.fail(fmt.Errorf("job_info response has no result: %q", raw))
		}
		s.rpcID++
		s.jobState = infos[0].JobState
		switch s.jobState {
		case "finished":
			s.state = stateFinished
		case "aborted":
			return s.fail(ErrAborted)
		}
	}
	return nil
}

func (s *PrintSession) fail(err error) error {
	s.state = stateFailed
	return err
}
