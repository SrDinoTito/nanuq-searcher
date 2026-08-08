package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"nanuq-engine/internal/config"
)

// RegisterConfig wires the GET /engine_descriptions.json route (REQ-017,
// TASK-012c), porting webapp.py engine_descriptions() (L1063-1087).
//
// The file is named config.go to group the machine-readable configuration
// endpoints; the public /config route stays in misc.go (TASK-012a) and is
// NOT duplicated here.
//
// TODO(TASK-011/022): engines are not wired into the webapp yet, so the
// endpoint serves an empty list. It will be populated with per-engine
// descriptions once the registry is connected.
func RegisterConfig(mux *http.ServeMux, _ *config.Config) {
	mux.HandleFunc("/engine_descriptions.json", engineDescriptionsHandler)
}

// engineDescriptionsHandler serves the per-engine descriptions as JSON.
// No engines are loaded this phase (TASK-011/022), so the response is an
// empty array serialised as [] rather than null.
func engineDescriptionsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	body, err := json.Marshal([]string{})
	if err != nil {
		slog.Error("engine_descriptions: marshal", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(body)
}
