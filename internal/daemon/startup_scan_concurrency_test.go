package daemon

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestConcurrentStartupScanRefreshesShareOneScan(t *testing.T) {
	var calls atomic.Int32
	var active atomic.Int32
	var maxActive atomic.Int32
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	state := newStartupScanState(func(context.Context) recovery.PlanResult {
		calls.Add(1)
		current := active.Add(1)
		for {
			observed := maxActive.Load()
			if current <= observed || maxActive.CompareAndSwap(observed, current) {
				break
			}
		}
		defer active.Add(-1)
		close(firstEntered)
		<-releaseFirst
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
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent refresh was not coalesced; calls=%d", got)
	}
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("recovery scans overlapped; max active=%d", got)
	}
}

func TestStartupScanRefreshWaitHonorsContextDeadline(t *testing.T) {
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	ownerDone := make(chan struct{})
	state := newStartupScanState(func(context.Context) recovery.PlanResult {
		select {
		case <-firstEntered:
		default:
			close(firstEntered)
			<-releaseFirst
		}
		return recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "tun-interface", Target: "podlaz0", Description: "existing raw evidence"}}}
	})
	go func() {
		defer close(ownerDone)
		state.Refresh(context.Background())
	}()
	<-firstEntered

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := state.Refresh(ctx)
	elapsed := time.Since(started)
	close(releaseFirst)
	<-ownerDone

	if elapsed > 100*time.Millisecond {
		t.Fatalf("refresh wait ignored context deadline: %s", elapsed)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[len(result.Warnings)-1].Message, "concurrent recovery scan") {
		t.Fatalf("timed-out shared refresh must publish an inspection warning: %#v", result.Warnings)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("timed-out authoritative refresh must not republish stale candidates: %#v", result.Candidates)
	}
}

func TestStartupScanRefreshPublishesIncompleteWhenAuthoritativeScanExceedsDeadline(t *testing.T) {
	state := newStartupScanState(func(ctx context.Context) recovery.PlanResult {
		<-ctx.Done()
		return recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "dns-link", Target: "podlaz0", Description: "stale result after timeout"}}}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := state.Refresh(ctx)
	if len(result.Candidates) != 0 {
		t.Fatalf("expired authoritative scan must not publish candidates: %#v", result.Candidates)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0].Message, "authoritative refresh did not complete") {
		t.Fatalf("expired authoritative scan must publish incomplete evidence: %#v", result.Warnings)
	}
	if snapshot := state.Snapshot(); len(snapshot.Candidates) != 0 || len(snapshot.Warnings) == 0 {
		t.Fatalf("published snapshot must remain incomplete without stale candidates: %#v", snapshot)
	}
}
