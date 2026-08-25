package protocol_test

import (
	"fmt"

	"github.com/cabbagekobe/miotto/protocol"
)

func ExampleFrameJSON() {
	fmt.Printf("%s\n", protocol.FrameJSON("job_info", []int{92}, 41))
	// Output:
	// cmd json
	// {"method":"job_info","params":[92],"id":41}
}

func ExampleParseJSONResponse() {
	resp := protocol.ParseJSONResponse([]byte(`{"result":{"job_id":92},"id":1}`))
	fmt.Println(*resp.ID, string(resp.Result))
	// Output:
	// 1 {"job_id":92}
}
