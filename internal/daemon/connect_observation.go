package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	productstatus "github.com/AidarKhusainov/podlaz/internal/status"
)

// Experimental, observation only. No event or write result is lifecycle authority.
// A consumer MUST reject a missing terminal, sequence gap, nonzero loss count,
// unknown required field or expired window. Aliases are local to this file, not
// profile IDs, health generations, transaction IDs or cleanup authority.
const connectObservationWindow = 15 * time.Minute
const connectObservationLimit = 1024

type connectObservationEvent struct {
	Source           string `json:"source"`
	Schema           int    `json:"schema"`
	Operation        uint64 `json:"operation"`
	Sequence         uint64 `json:"sequence"`
	ElapsedUS        int64  `json:"elapsed_us"`
	Role             string `json:"role"`
	Generation       string `json:"generation"`
	Pass             string `json:"pass"`
	Event            string `json:"event"`
	Outcome          string `json:"outcome"`
	Phase            string `json:"phase"`
	Classification   string `json:"classification"`
	DNSCause         string `json:"dns_cause"`
	Rollback         string `json:"rollback"`
	Lost             uint64 `json:"lost_before"`
	Session          string `json:"session,omitempty"`
	Protection       string `json:"protection_authority,omitempty"`
	LiveProtection   string `json:"live_protection,omitempty"`
	Lifecycle        string `json:"lifecycle,omitempty"`
	ProductState     string `json:"product_state,omitempty"`
	HealthGeneration uint64 `json:"health_generation,omitempty"`
	ActiveGeneration string `json:"active_generation,omitempty"`
}

type connectObservation struct {
	source      string
	until       time.Time
	started     time.Time
	operations  atomic.Uint64
	generations atomic.Uint64
	events      chan connectObservationEvent
}
type connectObservationOperation struct {
	observer     *connectObservation
	id           uint64
	sequence     atomic.Uint64
	lost         atomic.Uint64
	transactions map[string]string
}
type connectObservationScope struct {
	operation *connectObservationOperation
	// B is the requested candidate; A is its built-in previous restoration.
	role       string
	generation string
	pass       string
}
type connectObservationKey struct{}

func newConnectObservation(runtimeDir string) *connectObservation {
	now := time.Now()
	seconds, err := strconv.ParseInt(os.Getenv("PODLAZ_CONNECT_OBSERVATION_UNTIL"), 10, 64)
	if err != nil {
		return nil
	}
	until := time.Unix(seconds, 0)
	if !until.After(now) || until.Sub(now) > connectObservationWindow {
		return nil
	}
	tick, err := rollbackChildProcessStartTime(os.Getpid())
	if err != nil {
		return nil
	}
	// Existing exact PID/start-time parsing; absence is never invented identity.
	source := strconv.Itoa(os.Getpid()) + "-" + tick
	o := &connectObservation{source: source, until: now.Add(until.Sub(now)), started: now, events: make(chan connectObservationEvent, 128)}
	go o.write(runtimeDir)
	return o
}

func (o *connectObservation) begin(ctx context.Context) context.Context {
	if o == nil || !time.Now().Before(o.until) {
		return ctx
	}
	id := o.operations.Add(1)
	if id > 16 {
		return ctx
	}
	scope := &connectObservationScope{operation: &connectObservationOperation{observer: o, id: id, transactions: make(map[string]string)}, role: "B", generation: "unknown"}
	return context.WithValue(ctx, connectObservationKey{}, scope)
}
func connectObservationFrom(ctx context.Context) *connectObservationScope {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(connectObservationKey{}).(*connectObservationScope)
	return s
}
func connectObservationPrevious(ctx context.Context) context.Context {
	s := connectObservationFrom(ctx)
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, connectObservationKey{}, &connectObservationScope{operation: s.operation, role: "A", generation: "unknown"})
}
func bindConnectObservationGeneration(ctx context.Context, transactionID string) {
	if s := connectObservationFrom(ctx); s != nil {
		s.generation = strconv.FormatUint(s.operation.observer.generations.Add(1), 10)
		s.operation.transactions[transactionID] = s.generation
		observeConnect(ctx, "generation-bound", "succeeded", nil)
	}
}
func observeConnect(ctx context.Context, event, outcome string, err error) {
	s := connectObservationFrom(ctx)
	if s == nil {
		return
	}
	e := connectObservationEvent{Event: event, Outcome: outcome, Phase: "unknown", Classification: "none", DNSCause: "unknown", Rollback: "unknown"}
	if err != nil {
		phase, _, rollback := tunFailureLogFields(err)
		e.Phase = connectObservationPhase(phase)
		e.Classification = string(tunLifecycleFailureClassification(e.Phase, err))
		if cause := netexecutor.ResolvedScopedQueryFailureCause(err); cause != "" {
			e.DNSCause = cause
		}
		switch rollback {
		case "not-started", "completed", "failed":
			e.Rollback = rollback
		}
	}
	s.emit(e)
}
func (s *connectObservationScope) emit(e connectObservationEvent) {
	o := s.operation.observer
	seq := s.operation.sequence.Add(1)
	if seq > connectObservationLimit || !time.Now().Before(o.until) {
		s.operation.lost.Add(1)
		return
	}
	e.Schema = 1
	e.Source = o.source
	if e.Source == "" {
		e.Source = "unknown"
	}
	e.Operation = s.operation.id
	e.Sequence = seq
	e.ElapsedUS = time.Since(o.started).Microseconds()
	e.Role = s.role
	e.Generation = s.generation
	e.Pass = s.pass
	if e.Pass == "" {
		e.Pass = "unknown"
	}
	e.Lost = s.operation.lost.Load()
	select {
	case o.events <- e:
	default:
		s.operation.lost.Add(1)
	}
}
func observedConnectCall(ctx context.Context, event string, fn func() error) error {
	observeConnect(ctx, event+"-start", "started", nil)
	err := fn()
	outcome := "succeeded"
	if err != nil {
		outcome = "failed"
	}
	observeConnect(ctx, event+"-end", outcome, err)
	return err
}

// Only fixed internal phase names enter the private stream. Even arbitrary
// wrapped error text and verification summaries are deliberately never read.
func connectObservationPhase(phase string) string {
	switch phase {
	case "preflight", "transaction-begin", "core-preflight", "core-config-preflight", "core-start", "handoff", "server-bypass", "recovery",
		"dns-ownership-preflight", "tun-address-verify", "network-apply", "network-verify", "connectivity-verify", "privacy-envelope-preflight",
		"privacy-envelope-replace", "privacy-envelope-arm", "privacy-envelope-verify", "privacy-connectivity-verify", "commit",
		"route-verify", "tcp-verify", "resolved-link-verify", "resolved-link-query-verify", "system-resolver-verify", "dns-route-verify":
		return phase
	default:
		return "unknown"
	}
}

func (l *networkSessionLifecycle) finishConnectObservation(ctx context.Context, err error) {
	s := connectObservationFrom(ctx)
	if s == nil {
		return
	}
	e := connectObservationEvent{Event: "operation-end", Outcome: "succeeded", Phase: "unknown", Classification: "unknown", DNSCause: "unknown", Rollback: "unknown", Session: "unknown", Protection: "unknown", LiveProtection: "unknown", Lifecycle: "returned-success"}
	if err != nil {
		e.Outcome = "failed"
		e.Lifecycle = "returned-failure"
	}
	// These top-level errors have explicit lifecycle meaning. Do not errors.As a
	// joined B/A chain for phase or DNS cause at this aggregate boundary.
	switch err.(type) {
	case protectedReplacementCleanupError:
		e.Lifecycle = "cleanup-incomplete-recovery-required"
	case protectedPreviousGenerationRestoreError:
		e.Lifecycle = "restore-failed-recovery-required"
	}
	l.continuationMu.Lock()
	state, exists, loadErr := l.continuation.stateStore().Load()
	l.continuationMu.Unlock()
	if loadErr == nil {
		if !exists {
			e.Session = "absent"
			e.Protection = "absent"
		} else {
			switch state.Intent {
			case networkSessionIntentResume, networkSessionIntentDisconnect, networkSessionIntentTerminal:
				e.Session = string(state.Intent)
			}
			if state.Protection == nil {
				e.Protection = "absent"
			} else {
				switch state.Protection.State {
				case networkSessionProtectionUnarmed, networkSessionProtectionArming, networkSessionProtectionArmed, networkSessionProtectionRemoving:
					e.Protection = string(state.Protection.State)
				}
			}
		}
	}
	if l.verifiedStatus != nil {
		status := l.verifiedStatus(context.WithoutCancel(ctx))
		e.ProductState = string(productstatus.WithTunHealth(productstatus.FromDaemon(status), status.TunHealth).ProductView(nil).State)
		e.ActiveGeneration = "unknown"
		if alias, ok := s.operation.transactions[status.ActiveTransactionID]; ok {
			e.ActiveGeneration = alias
		}
		if status.TunHealth != nil {
			e.HealthGeneration = status.TunHealth.NetworkGeneration
		}
	} else {
		e.ProductState = "Unknown"
		e.ActiveGeneration = "unknown"
	}
	// Durable armed metadata is NOT proof of current live composition. The
	// operation's envelope-verify and commit events are the independent evidence.
	s.emit(e)
}

func (o *connectObservation) write(runtimeDir string) {
	root, err := os.OpenRoot(runtimeDir)
	if err != nil {
		return
	}
	defer root.Close()
	if err := root.Mkdir("diagnostics", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return
	}
	info, err := root.Lstat("diagnostics")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	// O_EXCL refuses stale files, symlinks and devices. No diagnostic failure
	// removes/replaces anything, retries a write, or reaches the lifecycle caller.
	file, err := root.OpenFile("diagnostics/connect-observation.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	timer := time.NewTimer(time.Until(o.until))
	defer timer.Stop()
	bytesWritten := 0
	for count := 0; count < connectObservationLimit; count++ {
		select {
		case <-timer.C:
			return
		case e := <-o.events:
			data, err := json.Marshal(e)
			if err != nil {
				return
			}
			data = append(data, '\n')
			if bytesWritten+len(data) > 512*1024 {
				return
			}
			n, err := file.Write(data)
			if err != nil || n != len(data) {
				return
			}
			bytesWritten += n
		}
	}
}
