package mqtt

import (
	"encoding/json"
	"testing"

	"bticino_bridge/pkg/access"
)

func TestAccessEventPayload(t *testing.T) {
	ev := access.AccessEvent{
		Type: access.TypeVisitor, Confidence: access.ConfHigh,
		Where: "20", HadCall: true, RingSource: "doorbell_entrance", DurationS: 2.5,
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(accessEventPayload(ev)), &m); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	// HA requires the event_type field in the state_topic payload.
	if m["event_type"] != "visitor" {
		t.Errorf("event_type = %v, want visitor", m["event_type"])
	}
	if m["had_call"] != true {
		t.Errorf("had_call = %v, want true", m["had_call"])
	}
	if m["where"] != "20" {
		t.Errorf("where = %v, want 20", m["where"])
	}
}
