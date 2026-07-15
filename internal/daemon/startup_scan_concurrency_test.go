package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestStartupScanRefreshesAreSerialized(t *testing.T) {
	var calls atomic.Int32
	var active atomic.Int32
	var maxActive atomic.Int32
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	state := newStartupScanState(func(context.Context) recovery.PlanResult {
		call := calls.Add(1)
		current := active.Add(1)
		for {
			observed := maxActive.Load()
			if current <= observed || maxActive.CompareAndSwap(observed, current) {
				break
			}
		}
		defer active.Add(-1)
		if call == 1 {
			close(firstEntered)
			<-releaseFirst
		}
		return recovery.PlanResult{}
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		state.Refresh(context.Background())
	}()
	<-firstEntered
	go func() {
		defer wg.Done()
		state.Refresh(context.Background())
	}()

	time.Sleep(25 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("second recovery scan started concurrently; calls=%d", got)
	}
	close(releaseFirst)
	wg.Wait()
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected both refreshes to complete, calls=%d", got)
	}
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("recovery scans overlapped; max active=%d", got)
	}
}
