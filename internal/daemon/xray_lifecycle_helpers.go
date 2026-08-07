package daemon

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func profileFromSnapshot(p api.ProfileSnapshot) profile.Profile {
	return profile.Profile{
		ID:               p.ID,
		Name:             p.Name,
		Source:           profile.SourceType(p.Source),
		Engine:           profile.Engine(p.Engine),
		Server:           p.Server,
		Port:             p.Port,
		Protocol:         p.Protocol,
		UserIdentity:     p.UserIdentity,
		Transport:        p.Transport,
		Security:         p.Security,
		Encryption:       p.Encryption,
		Flow:             p.Flow,
		ServerName:       p.ServerName,
		ALPN:             p.ALPN,
		Fingerprint:      p.Fingerprint,
		Path:             p.Path,
		HostHeader:       p.HostHeader,
		ServiceName:      p.ServiceName,
		RealityPublicKey: p.RealityPublicKey,
		RealityShortID:   p.RealityShortID,
		RealitySpiderX:   p.RealitySpiderX,
	}
}

func proxyListenersLine(listeners []planner.Listener) string {
	parts := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		parts = append(parts, fmt.Sprintf("%s (%s)", listener.Endpoint(), strings.ToUpper(listener.Protocol)))
	}
	if len(parts) == 0 {
		return "inactive"
	}
	return "listening on " + strings.Join(parts, ", ")
}

func processExitMessage(err error) string {
	if err == nil {
		return "Xray process exited unexpectedly"
	}
	return "Xray process exited unexpectedly: " + err.Error()
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// coreLogWriter is a privacy boundary for untrusted child stdout/stderr.
// It deliberately discards payload bytes and emits at most one low-cardinality
// structural event per stream after the child PID is known. Profile metadata,
// endpoints, identifiers, paths, and opaque child text never cross into journald.
type coreLogWriter struct {
	mu             sync.Mutex
	pid            int
	pidKnown       bool
	streamName     string
	outputObserved bool
	outputLogged   bool
}

func newCoreLogWriter(streamName string) *coreLogWriter {
	switch streamName {
	case "stdout", "stderr":
	default:
		streamName = "unknown"
	}
	return &coreLogWriter{streamName: streamName}
}

func (w *coreLogWriter) setPID(pid int) {
	w.mu.Lock()
	w.pid = pid
	w.pidKnown = true
	shouldLog := w.outputObserved && !w.outputLogged
	if shouldLog {
		w.outputLogged = true
	}
	w.mu.Unlock()
	if shouldLog {
		w.logOutputObserved(pid)
	}
}

func (w *coreLogWriter) Write(p []byte) (int, error) {
	written := len(p)
	if written == 0 {
		return 0, nil
	}

	w.mu.Lock()
	w.outputObserved = true
	shouldLog := w.pidKnown && !w.outputLogged
	pid := w.pid
	if shouldLog {
		w.outputLogged = true
	}
	w.mu.Unlock()
	if shouldLog {
		w.logOutputObserved(pid)
	}
	return written, nil
}

func (w *coreLogWriter) Flush() {
	w.mu.Lock()
	shouldLog := w.outputObserved && w.pidKnown && !w.outputLogged
	pid := w.pid
	if shouldLog {
		w.outputLogged = true
	}
	w.mu.Unlock()
	if shouldLog {
		w.logOutputObserved(pid)
	}
}

func (w *coreLogWriter) logOutputObserved(pid int) {
	log.Printf("podlazd: core xray %s output received pid=%d", w.streamName, pid)
}
