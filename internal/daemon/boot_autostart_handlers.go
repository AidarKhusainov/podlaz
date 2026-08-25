package daemon

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func registerBootAutostartHandlers(mux *http.ServeMux, store bootAutostartManifestStore, authorizers ...Authorizer) {
	authorizer := Authorizer(AllowAuthorizer{})
	if len(authorizers) > 0 && authorizers[0] != nil {
		authorizer = authorizers[0]
	}

	mux.HandleFunc(api.AutostartConfigurePath, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var request api.AutostartConfigureRequest
			if err := decodeJSONBody(r, &request); err != nil {
				writeDaemonAPIHTTPError(w, daemonAPIBadRequest(errors.New("invalid autostart configuration request")))
				return
			}
			if err := api.ValidateAutostartConfigureRequest(request); err != nil {
				writeDaemonAPIHTTPError(w, daemonAPIBadRequest(errors.New("invalid autostart configuration request")))
				return
			}
			if err := authorizeHTTPRequest(r, authorizer, ActionConfigureAutostart); err != nil {
				writeAuthorizationHTTPError(w, err)
				return
			}
			manifest, err := store.Enable(request)
			if err != nil {
				writeDaemonAPIHTTPError(w, daemonAPIInternal(errors.New("boot autostart configuration could not be saved")))
				return
			}
			writeBootAutostartStatus(w, bootAutostartStatusFromManifest(manifest))

		case http.MethodDelete:
			if err := authorizeHTTPRequest(r, authorizer, ActionConfigureAutostart); err != nil {
				writeAuthorizationHTTPError(w, err)
				return
			}
			if err := store.Disable(); err != nil {
				writeDaemonAPIHTTPError(w, daemonAPIInternal(errors.New("boot autostart configuration could not be disabled")))
				return
			}
			writeBootAutostartStatus(w, api.AutostartStatusResponse{})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc(api.AutostartStatusPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		manifest, exists, err := store.Load()
		if err != nil {
			writeDaemonAPIHTTPError(w, daemonAPIInternal(errors.New("boot autostart configuration is unavailable")))
			return
		}
		if !exists {
			writeBootAutostartStatus(w, api.AutostartStatusResponse{})
			return
		}
		writeBootAutostartStatus(w, bootAutostartStatusFromManifest(manifest))
	})
}

func bootAutostartStatusFromManifest(manifest bootAutostartManifest) api.AutostartStatusResponse {
	return api.AutostartStatusResponse{
		Enabled:     true,
		Mode:        manifest.Configuration.Mode,
		ProfileName: manifest.Configuration.Profile.Name,
	}
}

func writeBootAutostartStatus(w http.ResponseWriter, status api.AutostartStatusResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}
