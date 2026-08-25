// Package protocol assembles and parses wire frames. Pure functions only, no I/O.
//
// The frame formats were verified on the actual device (docs/protocol.md).
// Knowledge of "cmd json\n" and "cmd data EXTLEN=<len>\n" is confined to this package.
package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"regexp"
)

// MaxPacket is the maximum packet size in bytes (the firmware's getMaxPacketSize() = 0x2800).
const MaxPacket = 10240

// No trailing anchor: an event JSON may arrive concatenated after the ack.
var dataAck = regexp.MustCompile(`^cmd data EXTLEN=\d+ OK\b`)

// The field order of request becomes the JSON key order as-is.
// Do not change the order proven on the actual device.
type request struct {
	Method string `json:"method"`
	Params any    `json:"params"`
	ID     int    `json:"id"`
}

// FrameJSON assembles a JSON-RPC request frame.
// Pass []int or a struct as params (maps are unusable because their keys get sorted).
// Panics if params cannot be serialized (a programming error).
func FrameJSON(method string, params any, id int) []byte {
	// Inserting a length field makes the firmware return -8201 json parse error.
	body, err := json.Marshal(request{Method: method, Params: params, ID: id})
	if err != nil {
		panic(fmt.Sprintf("protocol: marshal request: %v", err))
	}
	return append([]byte("cmd json\n"), body...)
}

// FrameData assembles a data frame carrying one JPEG chunk.
// EXTLEN is the length including the 4 bytes of the job_id.
func FrameData(jobID uint32, chunk []byte) []byte {
	frame := fmt.Appendf(nil, "cmd data EXTLEN=%d\n", len(chunk)+4)
	frame = binary.LittleEndian.AppendUint32(frame, jobID)
	return append(frame, chunk...)
}

// MaxChunkSize returns the largest chunk length whose whole frame fits in MaxPacket.
func MaxChunkSize() int {
	// The header grows with the number of EXTLEN digits, so search for the
	// largest value whose whole frame still fits.
	size := MaxPacket
	for len(fmt.Sprintf("cmd data EXTLEN=%d\n", size+4))+4+size > MaxPacket {
		size--
	}
	return size
}

// Chunks splits payload into chunks no larger than MaxChunkSize.
// Each chunk shares memory with payload (no copying).
func Chunks(payload []byte) [][]byte {
	size := MaxChunkSize()
	var chunks [][]byte
	for i := 0; i < len(payload); i += size {
		chunks = append(chunks, payload[i:min(i+size, len(payload))])
	}
	return chunks
}

// Response is a JSON response extracted from one read.
// A reply to a request carries ID and Result (or Error); a device-initiated
// event carries Method and Params.
type Response struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	ID     *int            `json:"id"`
	Error  *RPCError       `json:"error"`
}

// RPCError is an error returned by the device
// (shared between the error-object form and the event.rpt_err params form).
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("%d %s", e.Code, e.Message)
}

// ParseJSONResponse extracts a JSON response from the bytes of one read.
// Returns nil if no JSON is present.
func ParseJSONResponse(raw []byte) *Response {
	// A data ack and JSON can share the same read, so start at the first "{".
	start := bytes.IndexByte(raw, '{')
	if start < 0 {
		return nil
	}
	// Multiple JSON objects can arrive concatenated; read only the first one.
	var resp Response
	if err := json.NewDecoder(bytes.NewReader(raw[start:])).Decode(&resp); err != nil {
		return nil
	}
	return &resp
}

// IsDataAck reports whether raw is a positive ack for a data frame
// ("cmd data EXTLEN=<n> OK").
func IsDataAck(raw []byte) bool {
	return dataAck.Match(raw)
}

// ErrorOf extracts the error carried by a response, or nil if it is not an error.
// Handles both the error-object form and the event.rpt_err form.
func ErrorOf(resp *Response) *RPCError {
	if resp.Error != nil {
		return resp.Error
	}
	if resp.Method == "event.rpt_err" {
		var e RPCError
		if err := json.Unmarshal(resp.Params, &e); err == nil && e.Message != "" {
			return &e
		}
	}
	return nil
}
