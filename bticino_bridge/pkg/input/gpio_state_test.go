package input

import (
	"io"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
)

// Run with -race: the GPIO poller writes states while the periodic monitor reads them.
func TestGPIOStatesReadWhileUpdated(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	im := NewInputMonitor(logger)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			im.updateGPIOState(i%13, i%2 == 0)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			im.GetCurrentGPIOStates()
		}
	}()
	wg.Wait()
}

func TestUpdateGPIOStateReportsPrevious(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	im := NewInputMonitor(logger)

	if _, known := im.updateGPIOState(180, true); known {
		t.Fatal("first state reported as known")
	}
	if old, known := im.updateGPIOState(180, false); !known || !old {
		t.Fatalf("got old=%t known=%t, want old=true known=true", old, known)
	}
}
