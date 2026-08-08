package handlers

import (
	"html/template"
	"log/slog"
	"net/http"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/webapp/templates"
)

// statsData is the template data for templates/stats.html.
type statsData struct {
	InstanceName string
}

// statsErrorsData is the template data for templates/stats_errors.html.
type statsErrorsData struct {
	InstanceName string
	Errors       []string
}

// statsTmpl and statsErrorsTmpl are the embedded statistics pages. Parse
// failures are build-time invariants: fail fast, like indexTmpl in misc.go.
var (
	statsTmpl       = template.Must(template.ParseFS(templates.FS, "stats.html"))
	statsErrorsTmpl = template.Must(template.ParseFS(templates.FS, "stats_errors.html"))
)

// RegisterStats wires the GET /stats and GET /stats/errors routes
// (REQ-017, TASK-012c), porting webapp.py stats() (L1088-1154).
//
// TODO(FASE F, TASK-020): accumulated per-engine metrics and error logs are
// not tracked yet; both pages render empty placeholders.
func RegisterStats(mux *http.ServeMux, cfg *config.Config) {
	mux.HandleFunc("/stats", statsHandler(cfg))
	mux.HandleFunc("/stats/errors", statsErrorsHandler(cfg))
}

// statsHandler renders the statistics overview page. No metrics are
// accumulated yet (TASK-020), so the page shows a placeholder. The instance
// name keeps the page usable while the data is empty.
func statsHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		data := statsData{InstanceName: cfg.General.InstanceName}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := statsTmpl.Execute(w, data); err != nil {
			slog.Error("stats: template execute", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}
}

// statsErrorsHandler renders the per-engine error page with an empty error
// list (TASK-020 will populate it).
func statsErrorsHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		data := statsErrorsData{
			InstanceName: cfg.General.InstanceName,
			Errors:       []string{},
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := statsErrorsTmpl.Execute(w, data); err != nil {
			slog.Error("stats errors: template execute", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}
}
