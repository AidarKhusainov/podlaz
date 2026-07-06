package daemon

import (
	"fmt"
	"strings"

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

type coreLogWriter struct{}

func newCoreLogWriter(_, _ string) *coreLogWriter { return &coreLogWriter{} }

func (w *coreLogWriter) setPID(int) {}

func (w *coreLogWriter) Write(p []byte) (int, error) { return len(p), nil }

func (w *coreLogWriter) Flush() {}
