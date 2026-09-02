package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func openDaemonListeners(runtimeDir string, authorizer Authorizer) ([]net.Listener, func(), error) {
	socketPath := api.SocketPath(runtimeDir)
	if err := removeStaleSocket(socketPath); err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("listen on daemon socket %s: %w", socketPath, err)
	}
	listeners := []net.Listener{listener}
	cleanup := func() {
		for i := len(listeners) - 1; i >= 0; i-- {
			_ = listeners[i].Close()
		}
		_ = os.Remove(socketPath)
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("set daemon socket permissions %s: %w", socketPath, err)
	}
	log.Printf("podlazd: daemon API listening on Unix socket")

	if shouldListenOnAbstractSocket(authorizer) {
		abstractListener, err := net.Listen("unix", api.AbstractSocketAddress())
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("listen on packaged daemon abstract socket: %w", err)
		}
		listeners = append(listeners, abstractListener)
		log.Printf("podlazd: packaged daemon API listening on abstract Unix socket")
	}
	return listeners, cleanup, nil
}

func (s Server) newHTTPServer(runtime *daemonRuntime, manifestStore bootAutostartManifestStore) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc(api.StatusPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("podlazd: status request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		status, scan := startupScanForPublication(
			r.Context(), runtime.currentStatus, runtime.lifecycle, runtime.startupScan, runtime.runtimeDir, unexpectedCoreExitRefreshTimeout,
		)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(withStartupScanStatus(status, scan))
		log.Printf("podlazd: status request handled")
	})
	mux.HandleFunc(api.DoctorPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("podlazd: doctor request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		mutationBefore := runtime.operationLock.doctorMutationSnapshot()
		lifecycleSnapshot := runtime.lifecycle.captureDoctorLifecycleSnapshot()
		status, scan := startupScanForPublication(
			r.Context(), runtime.currentStatus, runtime.lifecycle, runtime.startupScan, runtime.runtimeDir, unexpectedCoreExitRefreshTimeout,
		)

		var response api.DoctorResponse
		if s.Doctor != nil {
			response = s.Doctor(r.Context())
		} else {
			response = runtime.lifecycle.doctorFromSnapshot(r.Context(), lifecycleSnapshot)
		}
		lifecycleAfter := runtime.lifecycle.captureDoctorLifecycleSnapshot()
		mutationAfter := runtime.operationLock.doctorMutationSnapshot()

		stable := !mutationBefore.pending && !mutationAfter.pending && mutationBefore.generation == mutationAfter.generation
		if s.Doctor == nil {
			stable = doctorPublicationLifecycleStable(mutationBefore, mutationAfter, lifecycleSnapshot, lifecycleAfter, status)
		}
		if stable {
			response = withStartupScanDoctor(response, scan, status)
		} else {
			response = withIncompleteDoctorLifecycle(response, scan)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		log.Printf("podlazd: doctor request handled")
	})
	registerTunDiagnosticsHandler(mux, runtime.lifecycle)
	mux.HandleFunc(api.RecoverPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("podlazd: recover request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := authorizeHTTPRequest(r, runtime.authorizer, ActionRecoverExecute); err != nil {
			writeAuthorizationHTTPError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		response := runtime.operationLock.runRecoveryWithFollowUp(
			r.Context(),
			func() api.RecoveryResponse {
				blocked := runtime.startupMutationGate.Blocked()
				response := networkSessionRecoveryInitialStage(blocked, func() api.RecoveryResponse {
					return daemonRecover(r.Context(), runtime.runtimeDir, runtime.currentStatus(r.Context()))
				})
				if !blocked {
					refreshCtx, cancel := boundedStartupScanRefreshContext(r.Context())
					runtime.forceRefreshStartupScan(refreshCtx)
					cancel()
				}
				return response
			},
			func(response api.RecoveryResponse) api.RecoveryResponse {
				if !runtime.startupMutationGate.Blocked() || !networkSessionRecoveryConverged(response) {
					return response
				}

				var genericResponse api.RecoveryResponse
				_, retryErr := resumeNetworkSession(
					r.Context(),
					runtime.continuation,
					runtime.sessionLifecycle,
					runtime.currentStatus,
					func(recoveryCtx context.Context, status api.StatusResponse) api.RecoveryResponse {
						genericResponse = daemonRecover(recoveryCtx, runtime.runtimeDir, status)
						return genericResponse
					},
				)
				if genericResponse.Mode != "" {
					response = genericResponse
				}
				response = applyNetworkSessionResumeResult(response, runtime.startupMutationGate, retryErr)
				refreshCtx, cancel := boundedStartupScanRefreshContext(r.Context())
				runtime.forceRefreshStartupScan(refreshCtx)
				cancel()
				if retryErr != nil {
					log.Printf("podlazd: network session startup recovery remains incomplete after recovery request")
				}
				return response
			},
		)
		_ = json.NewEncoder(w).Encode(response)
		log.Printf("podlazd: recover request handled")
	})
	registerBootAutostartHandlers(mux, manifestStore, runtime.authorizer)
	registerLifecycleHandlers(mux, productPhaseLifecycle{
		inner:           runtime.startupMutationGate,
		tracker:         runtime.productPhase,
		terminalReasons: &runtime.productTerminalReasons,
	}, runtime.authorizer)

	return &http.Server{
		Handler: mux,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if subject, ok := peerSubjectFromConn(conn); ok {
				return contextWithPeerSubject(ctx, subject)
			}
			return ctx
		},
	}
}

func serveDaemonHTTP(httpServer *http.Server, listeners []net.Listener) <-chan error {
	errc := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func(ln net.Listener) {
			err := httpServer.Serve(ln)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
				return
			}
			errc <- nil
		}(ln)
	}
	return errc
}
