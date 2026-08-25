// Expected values are the exact byte sequences observed on the actual device.
// See docs/protocol.md.
package protocol_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/cabbagekobe/miotto/protocol"
)

func TestFrameJSONPrefixesHeaderWithoutLength(t *testing.T) {
	// Inserting a length field makes the firmware return -8201 json parse error.
	frame := protocol.FrameJSON("get_prop", []int{}, 7)
	want := []byte(`cmd json` + "\n" + `{"method":"get_prop","params":[],"id":7}`)
	if !bytes.Equal(frame, want) {
		t.Errorf("FrameJSON = %q, want %q", frame, want)
	}
}

func TestFrameJSONKeepsKeyOrderAndOmitsWhitespace(t *testing.T) {
	frame := protocol.FrameJSON("job_info", []int{92}, 41)
	want := []byte(`cmd json` + "\n" + `{"method":"job_info","params":[92],"id":41}`)
	if !bytes.Equal(frame, want) {
		t.Errorf("FrameJSON = %q, want %q", frame, want)
	}
}

func TestFrameDataLengthIncludesJobID(t *testing.T) {
	// Measured: a 10208-byte chunk yields EXTLEN=10212 (includes the 4 job_id bytes).
	chunk := bytes.Repeat([]byte("x"), 10208)
	frame := protocol.FrameData(92, chunk)
	header := []byte("cmd data EXTLEN=10212\n")
	if !bytes.HasPrefix(frame, header) {
		t.Fatalf("frame does not start with %q: %q...", header, frame[:32])
	}
	want := append([]byte{92, 0, 0, 0}, chunk...)
	if !bytes.Equal(frame[len(header):], want) {
		t.Error("frame body should be job_id (uint32 LE) + chunk")
	}
}

func TestFrameDataEncodesJobIDLittleEndian(t *testing.T) {
	frame := protocol.FrameData(0x01020304, nil)
	want := []byte("cmd data EXTLEN=4\n\x04\x03\x02\x01")
	if !bytes.Equal(frame, want) {
		t.Errorf("FrameData = %q, want %q", frame, want)
	}
}

func TestMaxChunkSizeKeepsPacketWithinLimit(t *testing.T) {
	size := protocol.MaxChunkSize()
	frame := protocol.FrameData(999999, bytes.Repeat([]byte("x"), size))
	if len(frame) > protocol.MaxPacket {
		t.Errorf("frame is %d bytes, exceeds MaxPacket %d", len(frame), protocol.MaxPacket)
	}
}

func TestChunksCoversPayloadExactly(t *testing.T) {
	payload := make([]byte, 25600)
	for i := range payload {
		payload[i] = byte(i)
	}
	chunks := protocol.Chunks(payload)
	if joined := bytes.Join(chunks, nil); !bytes.Equal(joined, payload) {
		t.Error("joined chunks differ from payload")
	}
	for i, c := range chunks {
		if len(c) == 0 || len(c) > protocol.MaxChunkSize() {
			t.Errorf("chunk %d has invalid length %d", i, len(c))
		}
	}
}

func TestParseJSONResponseExtractsResult(t *testing.T) {
	resp := protocol.ParseJSONResponse([]byte(`{"result":{"job_id":92},"id":100}`))
	if resp == nil {
		t.Fatal("response should parse")
	}
	if resp.ID == nil || *resp.ID != 100 {
		t.Errorf("ID = %v, want 100", resp.ID)
	}
	var result struct {
		JobID int `json:"job_id"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil || result.JobID != 92 {
		t.Errorf("Result = %s (err %v), want job_id 92", resp.Result, err)
	}
}

func TestParseJSONResponseSkipsLeadingAckText(t *testing.T) {
	// A data ack and JSON can arrive mixed in the same read.
	raw := []byte("cmd data EXTLEN=1091 OK\n" + `{"result":[],"id":35}`)
	resp := protocol.ParseJSONResponse(raw)
	if resp == nil {
		t.Fatal("response should parse")
	}
	if resp.ID == nil || *resp.ID != 35 {
		t.Errorf("ID = %v, want 35", resp.ID)
	}
	if string(resp.Result) != "[]" {
		t.Errorf("Result = %s, want []", resp.Result)
	}
}

func TestParseJSONResponseReadsFirstOfConcatenatedObjects(t *testing.T) {
	// Even when an event arrives concatenated after the reply in one read,
	// only the first object is returned.
	raw := []byte(`{"result":[{"job_id":92,"job_state":"printing"}],"id":5}` +
		`{"method":"event.rpt_prtsta","params":{"state":1}}`)
	resp := protocol.ParseJSONResponse(raw)
	if resp == nil {
		t.Fatal("concatenated JSON should parse")
	}
	if resp.ID == nil || *resp.ID != 5 {
		t.Errorf("ID = %v, want 5 (first object)", resp.ID)
	}
	if resp.Method != "" {
		t.Errorf("Method = %q, should be the first object, not the trailing event", resp.Method)
	}
}

func TestParseJSONResponseIdentifiesEvents(t *testing.T) {
	// Device-initiated events have a method and no id.
	resp := protocol.ParseJSONResponse([]byte(`{"method":"event.rpt_prtsta","params":{"state":1}}`))
	if resp == nil {
		t.Fatal("event should parse")
	}
	if resp.Method != "event.rpt_prtsta" {
		t.Errorf("Method = %q, want event.rpt_prtsta", resp.Method)
	}
	if resp.ID != nil {
		t.Errorf("ID = %v, events have no id", *resp.ID)
	}
}

func TestParseJSONResponseReturnsNilForNonJSON(t *testing.T) {
	if protocol.ParseJSONResponse([]byte("cmd data EXTLEN=10212 OK\n")) != nil {
		t.Error("ack text should parse to nil")
	}
	if protocol.ParseJSONResponse(nil) != nil {
		t.Error("empty input should parse to nil")
	}
}

func TestIsDataAck(t *testing.T) {
	if !protocol.IsDataAck([]byte("cmd data EXTLEN=10212 OK\n")) {
		t.Error("OK ack should be accepted")
	}
	if protocol.IsDataAck([]byte("cmd data EXTLEN=10212 ER\n")) {
		t.Error("ER response should be rejected")
	}
	if protocol.IsDataAck([]byte(`{"result":[],"id":1}`)) {
		t.Error("JSON should be rejected")
	}
	// The ack is still recognized when an event JSON arrives concatenated after it.
	if !protocol.IsDataAck([]byte("cmd data EXTLEN=10212 OK\n" + `{"method":"event.rpt_prtsta"}`)) {
		t.Error("ack with trailing JSON should be accepted")
	}
	if protocol.IsDataAck([]byte("cmd data EXTLEN=10212 OKAY\n")) {
		t.Error("OKAY should be rejected (word boundary)")
	}
}

func TestErrorOfReadsBothErrorShapes(t *testing.T) {
	// Responses carry errors in two shapes: an error object, or event.rpt_err.
	cases := []struct {
		raw     string
		code    int
		message string
	}{
		{`{"error":{"code":-8001,"message":"Job ID Ivalid"},"id":9}`, -8001, "Job ID Ivalid"},
		{
			`{"method":"event.rpt_err","params":{"code":-8201,"message":"json parse error"}}`,
			-8201, "json parse error",
		},
	}
	for _, tc := range cases {
		resp := protocol.ParseJSONResponse([]byte(tc.raw))
		if resp == nil {
			t.Fatalf("ParseJSONResponse(%q) = nil", tc.raw)
		}
		rpcErr := protocol.ErrorOf(resp)
		if rpcErr == nil || rpcErr.Code != tc.code || rpcErr.Message != tc.message {
			t.Errorf("ErrorOf(%q) = %v, want (%d, %q)", tc.raw, rpcErr, tc.code, tc.message)
		}
	}
}

func TestErrorOfReturnsNilForNonErrors(t *testing.T) {
	for _, raw := range []string{
		`{"result":{"job_id":1},"id":1}`,
		`{"method":"event.rpt_prtsta","params":{"state":1}}`, // an event other than rpt_err
	} {
		resp := protocol.ParseJSONResponse([]byte(raw))
		if resp == nil {
			t.Fatalf("ParseJSONResponse(%q) = nil", raw)
		}
		if rpcErr := protocol.ErrorOf(resp); rpcErr != nil {
			t.Errorf("ErrorOf(%q) = %v, want nil", raw, rpcErr)
		}
	}
}

func TestRPCErrorFormatsCodeAndMessage(t *testing.T) {
	err := &protocol.RPCError{Code: -8001, Message: "Job ID Ivalid"}
	if err.Error() != "-8001 Job ID Ivalid" {
		t.Errorf("Error() = %q", err.Error())
	}
}
