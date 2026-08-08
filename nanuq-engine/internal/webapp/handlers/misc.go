// Package handlers implements the HTTP handlers that serve the nanuq-engine
// web interface (TASK-012, REQ-017).
//
// Rendering uses only html/template (autoescaping always on, REQ-NF-005);
// text/template is never used to avoid XSS.
package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/webapp/templates"
)

// indexTmpl is the embedded index page. The file is compiled into the
// binary, so a parse failure is a build invariant: fail fast at startup
// rather than serve a broken page.
var indexTmpl = template.Must(template.ParseFS(templates.FS, "index.html"))

// opensearchDecl is the static XML declaration. It is written verbatim —
// it contains no template actions, so it never needs escaping. html/template
// would otherwise HTML-escape it as plain text ("&lt;?xml"), because the
// declaration is not a recognised element in its HTML lexer.
const opensearchDecl = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"

// opensearchDoc is the OpenSearch description document body (REQ-017).
// InstanceName is interpolated via html/template with autoescape on
// (REQ-NF-005); text/template is never used.
const opensearchDoc = `<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>{{.InstanceName}}</ShortName>
  <Description>Search results served by {{.InstanceName}}</Description>
  <Url type="text/html" method="get" template="/search?q={searchTerms}"></Url>
  <InputEncoding>UTF-8</InputEncoding>
</OpenSearchDescription>
`

var opensearchTmpl = template.Must(template.New("opensearch").Parse(opensearchDoc))

// RegisterMisc wires the miscellaneous routes of REQ-017 that do not belong
// to the search, config, preferences or stats groups (TASK-012a).
//
// All patterns use the Go 1.22 ServeMux syntax (DSG-013). The most specific
// pattern wins, so "/" acts as the catch-all for unmatched paths while more
// specific routes below take precedence.
func RegisterMisc(mux *http.ServeMux, cfg *config.Config) {
	mux.HandleFunc("/", indexHandler(cfg))
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/about", aboutHandler(cfg))
	mux.HandleFunc("/robots.txt", robotsHandler)
	mux.HandleFunc("/opensearch.xml", opensearchHandler(cfg))
	mux.HandleFunc("/manifest.json", manifestHandler(cfg))
	mux.HandleFunc("/favicon.ico", faviconHandler)
	mux.HandleFunc("/clear_cookies", clearCookiesHandler)
	mux.HandleFunc("/rss.xsl", rssXSLHandler)
	mux.HandleFunc("/client/{token}", clientCSSHandler)
	mux.HandleFunc("/logo/{resolution}", logoHandler)
	mux.HandleFunc("/config", configHandler(cfg))
}

// indexHandler renders the search form (GET /search). The "/" pattern also
// catches every path without a more specific route; those get a 404.
func indexHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := map[string]string{"InstanceName": cfg.General.InstanceName}
		if err := indexTmpl.Execute(w, data); err != nil {
			slog.Error("index: template execute", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}
}

// healthzHandler is the liveness probe: 200 "OK" (REQ-017).
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("OK"))
}

// aboutHandler serves a static about page (REQ-017). The upstream Python
// app redirects to /info/about; this base port serves plain text instead
// (static content, no template needed).
func aboutHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "%s is a private, privacy-respecting metasearch engine.\n", cfg.General.InstanceName)
	}
}

// robotsHandler serves the robots exclusion file (REQ-017).
func robotsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "User-agent: *\nAllow: /\n")
}

// opensearchHandler serves the OpenSearch description document (REQ-017).
func opensearchHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/opensearchdescription+xml; charset=utf-8")
		if _, err := w.Write([]byte(opensearchDecl)); err != nil {
			slog.Error("opensearch: write xml declaration", "err", err)
			return
		}
		data := map[string]string{"InstanceName": cfg.General.InstanceName}
		if err := opensearchTmpl.Execute(w, data); err != nil {
			slog.Error("opensearch: template execute", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}
}

// manifestHandler serves the web app manifest (REQ-017).
func manifestHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		body, err := json.Marshal(map[string]string{"name": cfg.General.InstanceName})
		if err != nil {
			slog.Error("manifest: marshal", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	}
}

// faviconHandler returns 204 No Content: no favicon is shipped yet
// (REQ-017). The upstream app serves a PNG; a real icon arrives in a
// later task.
func faviconHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// clearCookiesHandler expires every cookie the browser sent and redirects
// back to the index (REQ-017).
func clearCookiesHandler(w http.ResponseWriter, r *http.Request) {
	for _, c := range r.Cookies() {
		http.SetCookie(w, &http.Cookie{
			Name:   c.Name,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// rssXSLHandler is a 501 stub: the XSL stylesheet for RSS output is not
// implemented yet (REQ-017, TASK-012 later part).
func rssXSLHandler(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "rss.xsl is not implemented yet", http.StatusNotImplemented)
}

// clientCSSHandler answers any /client/<token>.css request with an empty
// stylesheet (REQ-017). Token-cache-busted theming arrives in a later task.
//
// Deviation from the TASK-012a wording: the pattern is "/client/{token}"
// (single wildcard segment) instead of "/client/{token}.css". Go 1.22
// ServeMux requires a wildcard to be an entire segment — a wildcard with
// literal text after it, e.g. "{token}.css", is rejected at registration
// (net/http pattern.go: "bad wildcard segment (must end with '}')"). The
// ".css" suffix is therefore validated here in the handler; any other
// /client/<token> request gets a 404.
func clientCSSHandler(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.PathValue("token"), ".css") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.WriteHeader(http.StatusOK)
}

// logoHandler is a 404 stub for /logo/<resolution> (REQ-017): logos are not
// shipped yet.
func logoHandler(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// configHandler serves the public instance configuration as JSON
// (REQ-017). Only values that are safe to expose publicly are included;
// secrets and engine credentials are never serialized here.
func configHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		payload := map[string]any{
			"instance_name":   cfg.General.InstanceName,
			"public_instance": cfg.Server.PublicInstance,
			"default_locale":  cfg.UI.DefaultLocale,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			slog.Error("config: marshal", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	}
}
