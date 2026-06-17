package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
)

type lifecycleService interface {
	Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error)
	Disconnect(context.Context) (api.LifecycleResponse, error)
}

func registerLifecycleHandlers(mux *http.ServeMux, lifecycle lifecycleService, authorizers ...Authorizer) {
	authorizer := Authorizer(AllowAuthorizer{})
	if len(authorizers) > 0 && authorizers[0] != nil {
		authorizer = authorizers[0]
	}
	mux.HandleFunc(api.ConnectPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("tunwardend: connect request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req api.ConnectRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := api.ValidateConnectRequest(req); err != nil {
			http.Error(w, err.Error(), lifecycleStatusCode(err))
			return
		}
		action, err := connectAuthorizationAction(req.Mode)
		if err != nil {
			http.Error(w, err.Error(), lifecycleStatusCode(err))
			return
		}
		if err := authorizeHTTPRequest(r, authorizer, action); err != nil {
			writeAuthorizationHTTPError(w, err)
			return
		}
		response, err := lifecycle.Connect(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), lifecycleStatusCode(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		log.Printf("tunwardend: connect request handled")
	})
	mux.HandleFunc(api.DisconnectPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("tunwardend: disconnect request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := authorizeHTTPRequest(r, authorizer, ActionDisconnect); err != nil {
			writeAuthorizationHTTPError(w, err)
			return
		}
		response, err := lifecycle.Disconnect(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		log.Printf("tunwardend: disconnect request handled")
	})
}

func connectAuthorizationAction(mode string) (AuthorizationAction, error) {
	switch strings.TrimSpace(mode) {
	case "":
		return "", errors.New("missing mode field")
	case planner.ModeProxyOnly:
		return ActionConnectProxyOnly, nil
	case planner.ModeTun:
		return ActionConnectTun, nil
	default:
		return "", fmt.Errorf("unsupported connect mode %q", mode)
	}
}

func decodeJSONBody(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON request body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON request body: trailing data")
	}
	return nil
}

func lifecycleStatusCode(err error) int {
	message := err.Error()
	if strings.Contains(message, "unsupported connect mode") || strings.Contains(message, "invalid profile") || strings.Contains(message, "missing ") {
		return http.StatusBadRequest
	}
	if strings.Contains(message, "connection already active") {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}
