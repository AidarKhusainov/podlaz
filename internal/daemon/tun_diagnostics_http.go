package daemon

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func registerTunDiagnosticsHandler(mux *http.ServeMux, lifecycle *XrayManager) {
	mux.HandleFunc(api.TunDoctorPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("podlazd: TUN diagnostics request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(lifecycle.TunDiagnostics(r.Context()))
		log.Printf("podlazd: TUN diagnostics request handled")
	})
}
