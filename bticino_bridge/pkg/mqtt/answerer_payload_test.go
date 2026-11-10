package mqtt

import (
	"encoding/json"
	"testing"
)

// The answerer sensor attributes carry the incoming call type (call_type),
// taken from the doorcall.incoming payload.
func TestAnswererIncomingAttrs(t *testing.T) {
	var m map[string]interface{}
	payload := answererIncomingAttrs(map[string]interface{}{"call_id": "c1", "call_type": "entrance"})
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if m["call_type"] != "entrance" {
		t.Errorf("call_type=%v, want entrance", m["call_type"])
	}
}

// Without call_type in the event (or no map at all), the attribute is
// "unknown", never empty.
func TestAnswererIncomingAttrs_NoType(t *testing.T) {
	for _, data := range []interface{}{nil, map[string]interface{}{"call_id": "c1"}, "garbage"} {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(answererIncomingAttrs(data)), &m); err != nil {
			t.Fatalf("not valid JSON: %v", err)
		}
		if m["call_type"] != "unknown" {
			t.Errorf("data=%v: call_type=%v, want unknown", data, m["call_type"])
		}
	}
}
