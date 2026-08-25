// Drives the whole print flow with fake responses. Byte-level correctness is
// covered by the protocol package tests.
package session_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/cabbagekobe/miotto/protocol"
	"github.com/cabbagekobe/miotto/session"
)

func testJPEG() []byte {
	payload := make([]byte, 25600) // three chunks' worth
	for i := range payload {
		payload[i] = byte(i)
	}
	return payload
}

func newSession(t *testing.T, jpeg []byte, copies int) *session.PrintSession {
	t.Helper()
	s, err := session.New(jpeg, copies)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewRejectsInvalidCopies(t *testing.T) {
	for _, copies := range []int{0, -1} {
		if _, err := session.New(testJPEG(), copies); err == nil {
			t.Errorf("New(copies=%d) should fail", copies)
		}
	}
}

func TestNewRejectsEmptyJPEG(t *testing.T) {
	// An empty byte slice (e.g. from a failed read) is a caller bug;
	// reject it before sending anything.
	for _, jpeg := range [][]byte{nil, {}} {
		if _, err := session.New(jpeg, 1); err == nil {
			t.Errorf("New(jpeg=%v) should fail", jpeg)
		}
	}
}

func TestTransferSurfacesRPCError(t *testing.T) {
	// When the reply to a chunk is a device error instead of an ack,
	// return it as an RPCError.
	s := newSession(t, testJPEG(), 1)
	s.NextRequest()
	if err := s.HandleResponse([]byte(`{"result":{"job_id":92},"id":1}`)); err != nil {
		t.Fatal(err)
	}
	s.NextRequest()
	err := s.HandleResponse(
		[]byte(`{"method":"event.rpt_err","params":{"code":-8100,"message":"printer busy"}}`))
	var rpcErr *session.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -8100 {
		t.Fatalf("want RPCError with code -8100, got %v", err)
	}
	if s.NextRequest() != nil {
		t.Error("session should be stopped")
	}
}

func TestNextRequestIsIdempotent(t *testing.T) {
	// NextRequest does not mutate state: it returns the same frame until a
	// response is handed over.
	s := newSession(t, testJPEG(), 1)
	if first, second := s.NextRequest(), s.NextRequest(); !bytes.Equal(first, second) {
		t.Errorf("create frames differ:\n%q\n%q", first, second)
	}
	if err := s.HandleResponse([]byte(`{"result":{"job_id":92},"id":1}`)); err != nil {
		t.Fatal(err)
	}
	if first, second := s.NextRequest(), s.NextRequest(); !bytes.Equal(first, second) {
		t.Errorf("data frames differ")
	}
}

func TestCreateResponseWithWrongIDFails(t *testing.T) {
	// Resending print_job would create another job, so an id mismatch fails
	// instead of being ignored.
	s := newSession(t, testJPEG(), 1)
	s.NextRequest()
	if err := s.HandleResponse([]byte(`{"result":{"job_id":92},"id":99}`)); err == nil {
		t.Fatal("want error for mismatched id in create")
	}
	if s.NextRequest() != nil {
		t.Error("session should be stopped")
	}
}

func TestPrintFlowReachesFinished(t *testing.T) {
	jpeg := testJPEG()
	s := newSession(t, jpeg, 1)

	// 1. print_job (params are the values proven on the device, key order included)
	want := []byte("cmd json\n" +
		`{"method":"print_job","params":{"job_type":0,"media_size":5011,"media_type":2010,` +
		fmt.Sprintf(`"copies":1,"channel":2,"file_size":%d},"id":1}`, len(jpeg)))
	if got := s.NextRequest(); !bytes.Equal(got, want) {
		t.Fatalf("print_job frame = %q, want %q", got, want)
	}
	if err := s.HandleResponse([]byte(`{"result":{"job_id":92},"id":1}`)); err != nil {
		t.Fatal(err)
	}

	// 2. Chunk transfer: each chunk waits for its ack before the next.
	var sent []byte
	for _, chunk := range protocol.Chunks(jpeg) {
		if got := s.NextRequest(); !bytes.Equal(got, protocol.FrameData(92, chunk)) {
			t.Fatalf("unexpected data frame for chunk of %d bytes", len(chunk))
		}
		sent = append(sent, chunk...)
		ack := fmt.Appendf(nil, "cmd data EXTLEN=%d OK\n", len(chunk)+4)
		if err := s.HandleResponse(ack); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(sent, jpeg) {
		t.Fatal("chunks do not cover the JPEG exactly")
	}

	// 3. job_info polling
	for i, state := range []string{"waiting", "downloading", "printing", "finished"} {
		if got := s.NextRequest(); !bytes.Equal(got, protocol.FrameJSON("job_info", []int{92}, 2+i)) {
			t.Fatalf("unexpected job_info frame at %q: %q", state, got)
		}
		response := fmt.Appendf(nil, `{"result":[{"job_id":92,"job_state":"%s"}],"id":%d}`, state, 2+i)
		if err := s.HandleResponse(response); err != nil {
			t.Fatal(err)
		}
		if s.JobState() != state {
			t.Errorf("JobState = %q, want %q", s.JobState(), state)
		}
	}

	if !s.Finished() {
		t.Error("session should be finished")
	}
	if s.NextRequest() != nil {
		t.Error("NextRequest after finish should be nil")
	}
}

func sessionInMonitor(t *testing.T) *session.PrintSession {
	t.Helper()
	s := newSession(t, bytes.Repeat([]byte("j"), 100), 1)
	s.NextRequest()
	if err := s.HandleResponse([]byte(`{"result":{"job_id":92},"id":1}`)); err != nil {
		t.Fatal(err)
	}
	s.NextRequest()
	if err := s.HandleResponse([]byte("cmd data EXTLEN=104 OK\n")); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPrintJobErrorStopsSession(t *testing.T) {
	s := newSession(t, testJPEG(), 1)
	s.NextRequest()
	err := s.HandleResponse(
		[]byte(`{"method":"event.rpt_err","params":{"code":-8201,"message":"json parse error"}}`))
	var rpcErr *session.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -8201 {
		t.Fatalf("want RPCError with code -8201, got %v", err)
	}
	if s.Finished() || s.NextRequest() != nil {
		t.Error("session should be stopped without finishing")
	}
}

func TestUnparseableCreateResponseStopsSession(t *testing.T) {
	s := newSession(t, testJPEG(), 1)
	s.NextRequest()
	if err := s.HandleResponse(nil); err == nil {
		t.Fatal("want error for unparseable response")
	}
	if s.NextRequest() != nil {
		t.Error("session should be stopped")
	}
}

func TestRejectedChunkStopsSession(t *testing.T) {
	s := newSession(t, testJPEG(), 1)
	s.NextRequest()
	if err := s.HandleResponse([]byte(`{"result":{"job_id":92},"id":1}`)); err != nil {
		t.Fatal(err)
	}
	s.NextRequest()
	if err := s.HandleResponse([]byte("cmd data EXTLEN=10218 ER\n")); err == nil {
		t.Fatal("want error for rejected chunk")
	}
	if s.NextRequest() != nil {
		t.Error("session should be stopped")
	}
}

func TestJobInfoErrorStopsSession(t *testing.T) {
	s := sessionInMonitor(t)
	s.NextRequest()
	err := s.HandleResponse([]byte(`{"error":{"code":-8001,"message":"Job ID Ivalid"},"id":2}`))
	var rpcErr *session.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -8001 {
		t.Fatalf("want RPCError with code -8001, got %v", err)
	}
	if s.NextRequest() != nil {
		t.Error("session should be stopped")
	}
}

func TestAbortedJobStopsSession(t *testing.T) {
	s := sessionInMonitor(t)
	s.NextRequest()
	err := s.HandleResponse([]byte(`{"result":[{"job_id":92,"job_state":"aborted"}],"id":2}`))
	if !errors.Is(err, session.ErrAborted) {
		t.Fatalf("want ErrAborted, got %v", err)
	}
	if s.JobState() != "aborted" {
		t.Errorf("JobState = %q, want aborted", s.JobState())
	}
	if s.NextRequest() != nil {
		t.Error("session should be stopped")
	}
}

func TestPendingJobResumesAndFinishes(t *testing.T) {
	// A job that went pending from running out of paper resumes automatically
	// once paper is loaded (measured). Pending keeps polling; it is not an error.
	s := sessionInMonitor(t)
	for i, state := range []string{"printing", "pending", "pending", "printing", "finished"} {
		s.NextRequest()
		response := fmt.Appendf(nil, `{"result":[{"job_id":92,"job_state":"%s"}],"id":%d}`, state, 2+i)
		if err := s.HandleResponse(response); err != nil {
			t.Fatalf("state %q: %v", state, err)
		}
		if s.JobState() != state {
			t.Errorf("JobState = %q, want %q", s.JobState(), state)
		}
	}
	if !s.Finished() {
		t.Error("session should be finished after resume")
	}
}

// finishMonitor drives a session in the monitor state to finished. It doubles
// as a check that polling continues right after an ignored response.
func finishMonitor(t *testing.T, s *session.PrintSession) {
	t.Helper()
	request := s.NextRequest()
	if !bytes.Contains(request, []byte(`"method":"job_info"`)) {
		t.Fatalf("next request should be job_info, got %q", request)
	}
	// Pick the id off the end of the job_info frame and echo it in the reply.
	var id int
	if _, err := fmt.Sscanf(string(request[bytes.LastIndexByte(request, ':')+1:]), "%d}", &id); err != nil {
		t.Fatalf("cannot read id from %q: %v", request, err)
	}
	response := fmt.Appendf(nil, `{"result":[{"job_id":92,"job_state":"finished"}],"id":%d}`, id)
	if err := s.HandleResponse(response); err != nil {
		t.Fatal(err)
	}
	if !s.Finished() {
		t.Fatal("session should be finished")
	}
}

func TestMonitorIgnoresUnsolicitedEvent(t *testing.T) {
	// Asynchronous events during printing (other than rpt_err) are ignored
	// and polling continues.
	s := sessionInMonitor(t)
	s.NextRequest()
	if err := s.HandleResponse([]byte(`{"method":"event.rpt_prtsta","params":{"state":1}}`)); err != nil {
		t.Fatalf("unsolicited event should be ignored, got %v", err)
	}
	finishMonitor(t, s)
}

func TestMonitorIgnoresMismatchedID(t *testing.T) {
	// A response whose id does not match our job_info is ignored and polling continues.
	s := sessionInMonitor(t)
	s.NextRequest()
	if err := s.HandleResponse([]byte(`{"result":[{"job_id":92,"job_state":"printing"}],"id":99}`)); err != nil {
		t.Fatalf("mismatched id should be ignored, got %v", err)
	}
	finishMonitor(t, s)
}

func TestMonitorFailsOnMatchingIDWithoutResult(t *testing.T) {
	// A matching id without a result is a real protocol violation, so fail.
	s := sessionInMonitor(t)
	s.NextRequest()
	if err := s.HandleResponse([]byte(`{"id":2}`)); err == nil {
		t.Fatal("want error for matching id without result")
	}
	if s.NextRequest() != nil {
		t.Error("session should be stopped")
	}
}

func TestCopiesIsForwardedToPrintJob(t *testing.T) {
	s := newSession(t, testJPEG(), 3)
	if request := s.NextRequest(); !bytes.Contains(request, []byte(`"copies":3`)) {
		t.Errorf("print_job frame should contain copies=3: %q", request)
	}
}
