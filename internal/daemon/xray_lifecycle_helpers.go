package daemon

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
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

type coreLogWriter struct {
	mu         sync.Mutex
	pid        int
	pidKnown   bool
	profileID  string
	streamName string
	pending    []byte
}

func newCoreLogWriter(profileID, streamName string) *coreLogWriter {
	return &coreLogWriter{profileID: profileID, streamName: streamName}
}

func (w *coreLogWriter) setPID(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pid = pid
	w.pidKnown = true
	w.flushCompleteLinesLocked()
}

func (w *coreLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := len(p)
	w.pending = append(w.pending, p...)
	if w.pidKnown {
		w.flushCompleteLinesLocked()
	}
	return written, nil
}

func (w *coreLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushCompleteLinesLocked()
	if len(w.pending) == 0 {
		return
	}
	w.logLineLocked(w.pending)
	w.pending = w.pending[:0]
}

func (w *coreLogWriter) flushCompleteLinesLocked() {
	for {
		idx := bytes.IndexByte(w.pending, '\n')
		if idx < 0 {
			return
		}
		w.logLineLocked(w.pending[:idx])
		copy(w.pending, w.pending[idx+1:])
		w.pending = w.pending[:len(w.pending)-idx-1]
	}
}

func (w *coreLogWriter) logLineLocked(line []byte) {
	cleanLine := strings.TrimRight(string(line), "\r")
	log.Printf("podlazd: core xray %s pid=%d profile=%s: %s", w.streamName, w.pid, render.Redact(w.profileID), render.Redact(cleanLine))
}
