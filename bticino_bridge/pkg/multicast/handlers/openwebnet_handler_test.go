package handlers

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestFloorPressVsStatusChatter(t *testing.T) {
	h := NewOpenWebNetHandler(nil, logrus.New())

	floor := h.parseOpenWebNetCommand("*7*59#14#0#0*##")
	if floor == nil || floor.EventType != "doorbell.floor" {
		t.Fatalf("press frame must map to doorbell.floor, got %+v", floor)
	}

	status := h.parseOpenWebNetCommand("*7*59#8#0#0*##")
	if status == nil || status.EventType == "doorbell.floor" {
		t.Fatalf("status chatter must not map to doorbell.floor, got %+v", status)
	}
	if status.EventType != "video.stream" {
		t.Fatalf("status chatter should fall through to video.stream, got %q", status.EventType)
	}
}
