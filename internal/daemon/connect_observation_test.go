package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

type observationTestLifecycle struct {
	connect func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error)
}

func (l observationTestLifecycle) Connect(ctx context.Context, r api.ConnectRequest) (api.LifecycleResponse, error) {
	return l.connect(ctx, r)
}
func (l observationTestLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}

type observationQueryRunner struct {
	output string
	err    error
}

func (r observationQueryRunner) Run(context.Context, string, ...string) (netexecutor.CommandResult, error) {
	return netexecutor.CommandResult{Stdout: r.output}, r.err
}

func readObservation(t *testing.T, dir string) []map[string]any {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		data, _ := os.ReadFile(filepath.Join(dir, "diagnostics", "connect-observation.jsonl"))
		var events []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var e map[string]any
			if json.Unmarshal([]byte(line), &e) == nil {
				events = append(events, e)
			}
		}
		if len(events) > 0 && events[len(events)-1]["event"] == "operation-end" {
			return events
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("missing daemon-owned terminal observation")
	return nil
}
func requireObservation(t *testing.T, events []map[string]any, role, event, outcome string) map[string]any {
	t.Helper()
	for _, e := range events {
		if e["role"] == role && e["event"] == event && e["outcome"] == outcome {
			return e
		}
	}
	t.Fatalf("missing %s/%s/%s in %#v", role, event, outcome, events)
	return nil
}

func TestConnectObservationSeparatesCandidateAndRestoreBeforeJoin(t *testing.T) {
	for _, restoreMode := range []string{"query", "pre-query", "success", "cancelled-client"} {
		t.Run(restoreMode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PODLAZ_CONNECT_OBSERVATION_UNTIL", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
			continuation := newNetworkSessionContinuationStore(dir, fixedBootID("boot-a"))
			previous := testContinuationRequest()
			if err := continuation.Save(previous); err != nil {
				t.Fatal(err)
			}
			protection := testArmedPrivacyProtection()
			if err := continuation.stateStore().SetProtection(&protection); err != nil {
				t.Fatal(err)
			}
			target := previous
			target.Handoff = api.HandoffReplacePodlaz
			target.Profile.ID = "target-example"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, bCause := (netexecutor.ResolvedScopedQueryVerifier{Runner: observationQueryRunner{output: "example.com: 2001:db8::1 -- link: podlaz0\n"}}).Query(ctx, "podlaz0", 7, "example.com")
			_, aCause := (netexecutor.ResolvedScopedQueryVerifier{Runner: observationQueryRunner{err: errors.New("private.example.test 192.0.2.4 stdout secret")}}).Query(ctx, "podlaz0", 9, "example.com")
			if netexecutor.ResolvedScopedQueryFailureCause(bCause) != "no-ipv4" || netexecutor.ResolvedScopedQueryFailureCause(aCause) != "command-failure" {
				t.Fatal("invalid independent fixture")
			}
			inner := observationTestLifecycle{connect: func(callCtx context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
				h := newFullTunnelRunnerHarness(t)
				r := h.runner()
				r.requirePrivacyEnvelope = true
				r.armPrivacyEnvelope = func(context.Context, planner.TunPlan) error { return nil }
				r.verifyPrivacyEnvelope = func(context.Context) error { return nil }
				r.cleanupPrivacyEnvelope = func(context.Context) error { return nil }
				if req.Profile.ID == target.Profile.ID {
					r.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
						return newTunVerificationError("resolved-link-query", "private.example.test", bCause)
					}
				} else {
					if callCtx.Err() != nil {
						t.Fatal("restoration inherited client cancellation")
					}
					if _, ok := callCtx.Deadline(); !ok {
						t.Fatal("restoration deadline disappeared")
					}
					switch restoreMode {
					case "query":
						r.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
							return newTunVerificationError("resolved-link-query", "private.example.test", aCause)
						}
					case "pre-query":
						r.preflightCore = func(context.Context) error { return errors.New("private.example.test early failure") }
					}
				}
				active, err := r.run(callCtx)
				if req.Profile.ID == target.Profile.ID {
					if e := continuation.stateStore().RestoreReplacement(); e != nil {
						t.Fatal(e)
					}
					if restoreMode == "cancelled-client" {
						cancel()
					}
				}
				return lifecycleResponse(active), err
			}}
			l := newNetworkSessionLifecycle(inner, continuation)
			_, err := l.Connect(ctx, target)
			if !errors.Is(err, bCause) {
				t.Fatalf("original B error lost: %v", err)
			}
			events := readObservation(t, dir)
			b := requireObservation(t, events, "B", "connect-end", "failed")
			if b["dns_cause"] != "no-ipv4" {
				t.Fatalf("B cause=%#v", b)
			}
			aOutcome := "succeeded"
			if restoreMode == "query" || restoreMode == "pre-query" {
				aOutcome = "failed"
			}
			a := requireObservation(t, events, "A", "restore-end", aOutcome)
			if restoreMode == "query" && a["dns_cause"] != "command-failure" {
				t.Fatalf("A cause mixed with B: %#v", a)
			}
			if restoreMode == "pre-query" && (a["phase"] != "core-preflight" || a["dns_cause"] != "unknown" || a["generation"] != "unknown") {
				t.Fatalf("early A attribution invented: %#v", a)
			}
			if a["operation"] != b["operation"] {
				t.Fatal("restore lost operation correlation")
			}
			if restoreMode != "pre-query" && a["generation"] == b["generation"] {
				t.Fatal("B and A generations conflated")
			}
			if restoreMode == "success" || restoreMode == "cancelled-client" {
				requireObservation(t, events, "A", "connectivity-1-end", "succeeded")
				requireObservation(t, events, "A", "connectivity-2-end", "succeeded")
			}
			data, _ := json.Marshal(events)
			for _, secret := range []string{"private.example.test", "192.0.2.4", "target-example", "profile-example", "stdout secret"} {
				if strings.Contains(string(data), secret) {
					t.Fatalf("diagnostic leaked %s", secret)
				}
			}
		})
	}
}

func TestConnectObservationDisabledDoesNotWrite(t *testing.T) {
	t.Setenv("PODLAZ_CONNECT_OBSERVATION_UNTIL", "")
	dir := t.TempDir()
	l := newNetworkSessionLifecycle(observationTestLifecycle{connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
		return api.LifecycleResponse{}, nil
	}}, newNetworkSessionContinuationStore(dir, fixedBootID("boot-a")))
	if _, err := l.Connect(context.Background(), testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "diagnostics", "connect-observation.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("disabled diagnostics created output: %v", err)
	}
}

// This is the release's separate known defect. Observation must see the original
// typed failure at the pass boundary even when rollback drops its outer summary.
func TestRollbackCompletionReleaseSummaryLossRemainsSeparate(t *testing.T) {
	cause := errors.New("underlying")
	summary := tunFailureDiagnosticSummary{PrimaryClassification: tundiag.ClassTimeout, ReportPath: "/run/podlaz/diagnostics/tun-last.json", Persisted: true}
	original := withTunFailureDiagnosticSummary(newTunVerificationError("resolved-link-query", "example", cause), summary)
	result := withTunRollbackCompleted(original)
	var verification *TunVerificationError
	if !errors.Is(result, cause) || !errors.As(result, &verification) || !verification.RollbackCompleted {
		t.Fatal("release error semantics changed")
	}
	if classification, _ := tunFailureDiagnosticLogFields(result); classification != "" {
		t.Fatal("known release defect changed; review separate correction")
	}
	disposition, mutation := classifyNetworkSessionReplayFailure(context.Background(), result)
	if fmt.Sprint(disposition) != "incomplete" || fmt.Sprint(mutation) != "rolled-back" {
		t.Fatalf("release replay semantics=%s/%s", disposition, mutation)
	}
}

func TestConnectObservationUsesExistingTerminalProjection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PODLAZ_CONNECT_OBSERVATION_UNTIL", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
	l := newNetworkSessionLifecycle(observationTestLifecycle{connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
		return api.LifecycleResponse{}, nil
	}}, newNetworkSessionContinuationStore(dir, fixedBootID("boot-a")))
	calls := 0
	l.verifiedStatus = func(context.Context) api.StatusResponse {
		calls++
		return api.StatusResponse{Connection: "active", Mode: planner.ModeTun, TunHealth: &api.TunHealthStatus{State: api.TunHealthRevalidating, NetworkGeneration: 7}, ProfileID: "private-profile", ProfileName: "private.example.test"}
	}
	if _, err := l.Connect(context.Background(), testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	terminal := readObservation(t, dir)
	e := terminal[len(terminal)-1]
	if calls != 1 || e["product_state"] != "Reconnecting" || e["health_generation"] != float64(7) || e["active_generation"] != "unknown" || e["live_protection"] != "unknown" {
		t.Fatalf("invented terminal authority: calls=%d event=%#v", calls, e)
	}
}

func TestConnectObservationFailureAndExpiryDoNotAffectLifecycle(t *testing.T) {
	for _, mode := range []string{"expired", "too-long", "write-refused"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			until := time.Now().Add(time.Minute)
			if mode == "expired" {
				until = time.Now().Add(-time.Minute)
			}
			if mode == "too-long" {
				until = time.Now().Add(time.Hour)
			}
			if mode == "write-refused" {
				if err := os.WriteFile(filepath.Join(dir, "diagnostics"), []byte("sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PODLAZ_CONNECT_OBSERVATION_UNTIL", strconv.FormatInt(until.Unix(), 10))
			sentinel := errors.New("original failure")
			calls := 0
			l := newNetworkSessionLifecycle(observationTestLifecycle{connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
				calls++
				return api.LifecycleResponse{}, sentinel
			}}, newNetworkSessionContinuationStore(dir, fixedBootID("boot-a")))
			_, err := l.Connect(context.Background(), testContinuationRequest())
			if calls != 1 || !errors.Is(err, sentinel) {
				t.Fatalf("diagnostics changed lifecycle: calls=%d err=%v", calls, err)
			}
			if mode != "write-refused" {
				if _, err := os.Stat(filepath.Join(dir, "diagnostics")); !os.IsNotExist(err) {
					t.Fatalf("invalid window wrote output: %v", err)
				}
			}
		})
	}
}

func TestConnectObservationFullQueueCannotBlockOrRetry(t *testing.T) {
	o := &connectObservation{until: time.Now().Add(time.Minute), started: time.Now(), events: make(chan connectObservationEvent, 1)}
	o.events <- connectObservationEvent{}
	ctx := o.begin(context.Background())
	calls := 0
	sentinel := errors.New("private failure")
	done := make(chan error, 1)
	go func() { done <- observedConnectCall(ctx, "connectivity-1", func() error { calls++; return sentinel }) }()
	select {
	case err := <-done:
		if err != sentinel || calls != 1 {
			t.Fatal("observer changed call result")
		}
	case <-time.After(time.Second):
		t.Fatal("observer blocked lifecycle")
	}
	if connectObservationFrom(ctx).operation.lost.Load() != 2 {
		t.Fatal("queue loss must be explicit")
	}
}

func TestConnectObservationFileIsPrivateAndCannotReplaceExistingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PODLAZ_CONNECT_OBSERVATION_UNTIL", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
	l := newNetworkSessionLifecycle(observationTestLifecycle{connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
		return api.LifecycleResponse{}, nil
	}}, newNetworkSessionContinuationStore(dir, fixedBootID("boot-a")))
	if _, err := l.Connect(context.Background(), testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	readObservation(t, dir)
	p := filepath.Join(dir, "diagnostics", "connect-observation.jsonl")
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe observation permissions: %v", err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	second := newConnectObservation(dir)
	ctx := second.begin(context.Background())
	observeConnect(ctx, "operation-end", "succeeded", nil)
	time.Sleep(20 * time.Millisecond)
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("observation overwrote prior evidence")
	}
}

type observationDeadlineRunner struct {
	check func(context.Context)
}

func (r observationDeadlineRunner) Run(ctx context.Context, _ string, _ ...string) (netexecutor.CommandResult, error) {
	r.check(ctx)
	return netexecutor.CommandResult{Stdout: "example.com: 192.0.2.1 -- link: podlaz0\n"}, nil
}

func TestConnectObservationRetainsScopedProbeAndCommandBudgets(t *testing.T) {
	oldRoute, oldDial, oldScoped, oldSystem := lookupTunRouteForProbe, dialTunProbeTarget, resolveTunDNSNameScoped, resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe, dialTunProbeTarget, resolveTunDNSNameScoped, resolveTunDNSName = oldRoute, oldDial, oldScoped, oldSystem
	}()
	lookupTunRouteForProbe = func(context.Context, string, string) error { return nil }
	dialTunProbeTarget = func(context.Context, string, uint16) error { return nil }
	resolveTunDNSName = func(context.Context, string) ([]string, error) { return []string{"192.0.2.1"}, nil }
	for _, enabled := range []bool{false, true} {
		for _, boundedParent := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled=%t/parent=%t", enabled, boundedParent), func(t *testing.T) {
				ctx := context.Background()
				cancel := func() {}
				if boundedParent {
					ctx, cancel = context.WithTimeout(ctx, time.Second)
				}
				defer cancel()
				parentDeadline, _ := ctx.Deadline()
				if enabled {
					o := &connectObservation{started: time.Now(), until: time.Now().Add(time.Minute), events: make(chan connectObservationEvent, 32)}
					ctx = o.begin(ctx)
				}
				calls := 0
				resolveTunDNSNameScoped = func(queryCtx context.Context, _ planner.TunAddressPlan, _ string) ([]string, error) {
					return (netexecutor.ResolvedScopedQueryVerifier{Runner: observationDeadlineRunner{check: func(commandCtx context.Context) {
						calls++
						deadline, ok := commandCtx.Deadline()
						if !ok {
							t.Fatal("lost DNS command deadline")
						}
						if boundedParent {
							if !deadline.Equal(parentDeadline) {
								t.Fatalf("changed caller cap: %v != %v", deadline, parentDeadline)
							}
						} else {
							remaining := time.Until(deadline)
							if remaining > 15*time.Second || remaining < 14900*time.Millisecond {
								t.Fatalf("changed 15s release budget: %v", remaining)
							}
						}
					}}}).Query(queryCtx, "podlaz0", 7, "example.com")
				}
				r := &fullTunnelTransactionRunner{plan: addressedTunPlanForConnectivityTest(), verifyConnectivity: verifyTunConnectivity}
				if err := r.verifyObservedConnectivity(ctx, "first"); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("DNS query retried: %d", calls)
				}
			})
		}
	}
}

func TestConnectObservationRecordsAuthoritativeDaemonProcessIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PODLAZ_CONNECT_OBSERVATION_UNTIL", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
	l := newNetworkSessionLifecycle(observationTestLifecycle{connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
		return api.LifecycleResponse{}, nil
	}}, newNetworkSessionContinuationStore(dir, fixedBootID("boot-a")))
	if _, err := l.Connect(context.Background(), testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	tick, err := rollbackChildProcessStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("%d-%s", os.Getpid(), tick)
	for _, event := range readObservation(t, dir) {
		if event["source"] != expected {
			t.Fatalf("missing exact source process: %#v", event["source"])
		}
	}
}


func TestConnectObservationSlowStatusDoesNotDelayLifecycle(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelled), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PODLAZ_CONNECT_OBSERVATION_UNTIL", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			deadline, _ := ctx.Deadline()
			sentinel := errors.New("lifecycle result")
			want := api.LifecycleResponse{Connection: "inactive", Mode: planner.ModeTun}
			var calls atomic.Int32
			l := newNetworkSessionLifecycle(observationTestLifecycle{connect: func(callCtx context.Context, _ api.ConnectRequest) (api.LifecycleResponse, error) {
				calls.Add(1)
				if got, ok := callCtx.Deadline(); !ok || got != deadline {
					t.Error("observer changed the product deadline")
				}
				if cancelled {
					cancel()
				}
				return want, sentinel
			}}, newNetworkSessionContinuationStore(dir, fixedBootID("boot-a")))
			blocked := make(chan struct{})
			var release sync.Once
			unblock := func() { release.Do(func() { close(blocked) }) }
			defer unblock()
			l.verifiedStatus = func(context.Context) api.StatusResponse {
				// XrayManager.Status performs filesystem inspection while holding
				// its mutex and does not use the supplied context.
				<-blocked
				return api.StatusResponse{Connection: "inactive"}
			}
			type result struct {
				response api.LifecycleResponse
				err      error
			}
			done := make(chan result, 1)
			go func() {
				response, err := l.Connect(ctx, testContinuationRequest())
				done <- result{response: response, err: err}
			}()
			var got result
			select {
			case got = <-done:
			case <-time.After(time.Second):
				t.Error("terminal observation delayed Connect on an unresponsive status read")
				unblock()
				select {
				case got = <-done:
				case <-time.After(time.Second):
					t.Fatal("Connect did not return after releasing the status read")
				}
			}
			unblock()
			if calls.Load() != 1 || got.err != sentinel || !reflect.DeepEqual(got.response, want) {
				t.Fatalf("diagnostics changed the lifecycle result: calls=%d response=%#v err=%v", calls.Load(), got.response, got.err)
			}
		})
	}
}

func TestConnectObservationTerminalSkipsUnavailableStream(t *testing.T) {
	for _, mode := range []string{"expired", "full-queue", "event-limit"} {
		t.Run(mode, func(t *testing.T) {
			store := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID("boot-a"))
			if err := store.Save(testContinuationRequest()); err != nil {
				t.Fatal(err)
			}
			stateReads, statusReads := 0, 0
			store.readBootID = func() (string, error) {
				stateReads++
				return "boot-a", nil
			}
			o := &connectObservation{started: time.Now(), until: time.Now().Add(time.Minute), events: make(chan connectObservationEvent, 1)}
			ctx := o.begin(context.Background())
			scope := connectObservationFrom(ctx)
			switch mode {
			case "expired":
				o.until = time.Now().Add(-time.Second)
			case "full-queue":
				o.events <- connectObservationEvent{}
			case "event-limit":
				scope.operation.sequence.Store(connectObservationLimit)
			}
			l := &networkSessionLifecycle{continuation: store, verifiedStatus: func(context.Context) api.StatusResponse {
				statusReads++
				return api.StatusResponse{Connection: "inactive"}
			}}
			l.finishConnectObservation(ctx, nil)
			if stateReads != 0 || statusReads != 0 {
				t.Fatalf("discarded terminal still inspected product state: state=%d status=%d", stateReads, statusReads)
			}
			if lost := scope.operation.lost.Load(); lost != 1 {
				t.Fatalf("discarded terminal did not record evidence loss: %d", lost)
			}
		})
	}
}

func TestConnectObservationStateReadCannotHoldLifecycle(t *testing.T) {
	store := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID("boot-a"))
	if err := store.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(blocked) }) }
	defer unblock()
	observedStore := store
	observedStore.readBootID = func() (string, error) {
		<-blocked
		return "boot-a", nil
	}
	o := &connectObservation{started: time.Now(), until: time.Now().Add(time.Minute), events: make(chan connectObservationEvent, 1)}
	ctx := o.begin(context.Background())
	l := &networkSessionLifecycle{continuation: observedStore}
	done := make(chan struct{})
	go func() {
		l.finishConnectObservation(ctx, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("terminal observation waited for a blocked durable-state read")
		unblock()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("terminal observation did not return after releasing the state read")
		}
		return
	}
	// Moving the same blocking read to a goroutine is insufficient if it keeps
	// the product's state mutation lock and stalls the following lifecycle.
	next := make(chan error, 1)
	go func() {
		_, _, err := store.stateStore().Load()
		next <- err
	}()
	select {
	case err := <-next:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Error("diagnostic state read held the product state lock after returning")
		unblock()
		select {
		case <-next:
		case <-time.After(time.Second):
			t.Fatal("product state read did not return after releasing diagnostics")
		}
	}
}

func TestConnectObservationFailedInspectionCannotInventTerminalAuthority(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PODLAZ_CONNECT_OBSERVATION_UNTIL", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
	want := api.LifecycleResponse{Connection: "active", Mode: planner.ModeTun}
	l := newNetworkSessionLifecycle(observationTestLifecycle{connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
		return want, nil
	}}, newNetworkSessionContinuationStore(dir, fixedBootID("boot-a")))
	l.verifiedStatus = func(context.Context) api.StatusResponse {
		return api.StatusResponse{Connection: "unknown", InspectionWarnings: []api.RecoveryWarning{{Target: "transaction state", Message: "private.example.test unreadable"}}}
	}
	got, err := l.Connect(context.Background(), testContinuationRequest())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("failed diagnostic inspection changed Connect: response=%#v err=%v", got, err)
	}
	events := readObservation(t, dir)
	terminal := events[len(events)-1]
	if terminal["product_state"] != "Unknown" || terminal["active_generation"] != "unknown" || terminal["live_protection"] != "unknown" || terminal["lifecycle"] != "returned-success" {
		t.Fatalf("failed inspection invented terminal evidence: %#v", terminal)
	}
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private.example.test") {
		t.Fatal("failed inspection leaked raw diagnostic text")
	}
}
