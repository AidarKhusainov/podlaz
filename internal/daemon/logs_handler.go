package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	podlogs "github.com/AidarKhusainov/podlaz/internal/logs"
)

type daemonLogsRunner func(context.Context, io.Writer, podlogs.Options) error
type daemonLogsPeerAuthorizer func(PeerSubject) error

func registerLogsHandler(mux *http.ServeMux) {
	registerLogsHandlerWithDeps(mux, podlogs.Run, authorizeDaemonLogsPeer)
}

func registerLogsHandlerWithDeps(mux *http.ServeMux, run daemonLogsRunner, authorize daemonLogsPeerAuthorizer) {
	if mux == nil {
		return
	}
	if run == nil {
		run = podlogs.Run
	}
	if authorize == nil {
		authorize = authorizeDaemonLogsPeer
	}

	mux.HandleFunc(api.LogsPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		subject, ok := peerSubjectFromContext(r.Context())
		if !ok {
			writeDaemonLogsAuthorizationError(w, fmt.Errorf("%w: daemon could not identify local logs peer", ErrAuthorizationUnavailable))
			return
		}
		if err := authorize(subject); err != nil {
			writeDaemonLogsAuthorizationError(w, err)
			return
		}

		opts, err := parseDaemonLogsOptions(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		stream := &daemonLogsHTTPWriter{dst: w, flush: opts.Follow}
		if err := run(r.Context(), stream, opts); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
				return
			}
			log.Printf("podlazd: daemon logs request failed")
			if stream.written == 0 {
				http.Error(w, "daemon logs are unavailable", http.StatusServiceUnavailable)
			}
		}
	})
}

func parseDaemonLogsOptions(r *http.Request) (podlogs.Options, error) {
	var opts podlogs.Options
	query := r.URL.Query()
	for key := range query {
		switch key {
		case "since", "follow", "core":
		default:
			return opts, fmt.Errorf("unsupported logs query parameter %q", key)
		}
	}

	if values, ok := query["since"]; ok {
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return opts, errors.New("logs since requires exactly one value")
		}
		canonical, err := podlogs.ParseSinceDuration(values[0])
		if err != nil {
			return opts, err
		}
		opts.Since = canonical
	}
	var err error
	if opts.Follow, err = daemonLogsQueryBool(query["follow"], "follow"); err != nil {
		return opts, err
	}
	if opts.Core, err = daemonLogsQueryBool(query["core"], "core"); err != nil {
		return opts, err
	}
	return opts, nil
}

func daemonLogsQueryBool(values []string, name string) (bool, error) {
	if len(values) == 0 {
		return false, nil
	}
	if len(values) != 1 || values[0] != "1" {
		return false, fmt.Errorf("logs %s must be exactly 1 when present", name)
	}
	return true, nil
}

func writeDaemonLogsAuthorizationError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrAuthorizationDenied) {
		http.Error(w, "daemon logs access denied", http.StatusForbidden)
		return
	}
	http.Error(w, "daemon logs authorization unavailable", http.StatusServiceUnavailable)
}

type daemonLogsHTTPWriter struct {
	dst     http.ResponseWriter
	flush   bool
	written int64
}

func (w *daemonLogsHTTPWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	w.written += int64(n)
	if err == nil && w.flush {
		if flusher, ok := w.dst.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return n, err
}
