package handlers

import (
	"html/template"
	"log/slog"
	"net/http"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/webapp/templates"
)

// preferencesData is the template data for templates/preferences.html.
type preferencesData struct {
	InstanceName  string
	DefaultLocale string
	Autocomplete  string
	SafeSearch    int
	Categories    []string
}

// defaultCategories is the static category list shown on the preferences
// page (REQ-017). It mirrors the standard category set of the upstream app.
// The engine catalog does not expose a category enumeration yet, so the list
// is fixed here; engine-driven categories arrive with TASK-011/022.
var defaultCategories = []string{
	"general", "images", "videos", "news", "it",
	"science", "files", "social media", "music", "map",
}

// preferencesTmpl is the embedded preferences page. Parse failures are
// build-time invariants: fail fast, like indexTmpl in misc.go.
var preferencesTmpl = template.Must(template.ParseFS(templates.FS, "preferences.html"))

// RegisterPreferences wires the GET /preferences route (REQ-017,
// TASK-012c), porting webapp.py preferences() (L858-985) to a minimal
// functional page: instance name, default locale, autocomplete, safe-search
// and the available categories are rendered read-only.
//
// TODO(TASK-012c): cookie-based preference persistence is not implemented
// in this phase; the page is informational only.
func RegisterPreferences(mux *http.ServeMux, cfg *config.Config) {
	mux.HandleFunc("/preferences", preferencesHandler(cfg))
}

// preferencesHandler renders the preferences page from the instance
// configuration. All values are escaped by html/template (REQ-NF-005).
func preferencesHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		data := preferencesData{
			InstanceName:  cfg.General.InstanceName,
			DefaultLocale: cfg.UI.DefaultLocale,
			Autocomplete:  cfg.Search.Autocomplete,
			SafeSearch:    cfg.Search.SafeSearch,
			Categories:    defaultCategories,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := preferencesTmpl.Execute(w, data); err != nil {
			slog.Error("preferences: template execute", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}
}
