// Package webapp implements the nanuq-engine HTTP front end (TASK-012).
//
// Routing uses the standard library http.ServeMux with the Go 1.22 pattern
// syntax (DSG-013): literal paths, whole-segment wildcards such as
// "/client/{token}" and "/logo/{resolution}", and most-specific-pattern-wins
// precedence. Templates are embedded at build time and rendered with
// html/template so autoescaping is always on (REQ-NF-005).
package webapp

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/limiter"
	"nanuq-engine/internal/search"
	"nanuq-engine/internal/webapp/handlers"
)

// Server is the HTTP front end: it owns the route table and the search
// service used by the /search handler.
type Server struct {
	cfg     *config.Config
	mux     *http.ServeMux
	svc     *search.SearchService
	store   search.BangResolver
	catalog search.EngineCatalog
	limiter limiter.Filter
}

// New builds a Server and registers the REQ-017 route table on a fresh
// http.ServeMux. svc, store and catalog may be nil during bootstrap: the
// misc routes never touch them, and /search responds 500 only when a
// request actually needs them (TASK-012b).
func New(cfg *config.Config, svc *search.SearchService, store search.BangResolver, catalog search.EngineCatalog) *Server {
	s := &Server{
		cfg:     cfg,
		mux:     http.NewServeMux(),
		svc:     svc,
		store:   store,
		catalog: catalog,
	}
	handlers.RegisterMisc(s.mux, cfg)
	handlers.RegisterPreferences(s.mux, cfg)
	handlers.RegisterStats(s.mux, cfg)
	handlers.RegisterConfig(s.mux, cfg)
	handlers.RegisterAutocomplete(s.mux, cfg)
	handlers.RegisterImageProxy(s.mux, cfg)
	s.mux.Handle("/search", handlers.SearchHandler(handlers.SearchDeps{
		Svc:     svc,
		Store:   store,
		Catalog: catalog,
		Formats: cfg.Search.Formats,
	}))
	return s
}

// Handler returns the root http.Handler: the fully-populated ServeMux,
// wrapped by the rate limiter middleware when one is installed.
func (s *Server) Handler() http.Handler {
	if s.limiter == nil {
		return s.mux
	}
	return handlers.LimiterMiddleware(s.limiter, s.mux)
}

// RegisterMetrics mounts an externally-built metrics endpoint (typically
// metrics.Handler() from internal/metrics) on the server mux under
// "/metrics" (REQ-014). It is mounted directly on the mux — outside the
// limiter middleware, whose protected paths only cover /search and
// /autocompleter — so /metrics stays reachable regardless of limiter
// configuration. Added in TASK-022 so cmd/nanuq-server can wire /metrics
// without reaching into the unexported mux.
func (s *Server) RegisterMetrics(h http.Handler) {
	if h == nil {
		return
	}
	s.mux.Handle("/metrics", h)
}

// WithLimiter instala el rate limiter IP (TASK-016). Es un no-op — devuelve s
// sin cambios — cuando l es nil o cfg.Server.Limiter es false: con el limiter
// deshabilitado el middleware no debe filtrar ninguna request (REQ-016). El
// wiring real (TASK-022) construye el *limiter.Limiter desde la config y lo
// inyecta aquí.
func (s *Server) WithLimiter(l limiter.Filter) *Server {
	if l == nil || !s.cfg.Server.Limiter {
		return s
	}
	s.limiter = l
	return s
}

// ListenAndServe binds cfg.Server.BindAddress:cfg.Server.Port and serves
// the route table. It returns only on error.
func (s *Server) ListenAndServe() error {
	addr := net.JoinHostPort(s.cfg.Server.BindAddress, strconv.Itoa(s.cfg.Server.Port))
	slog.Info("webapp listening", "addr", addr, "instance", s.cfg.General.InstanceName)
	return http.ListenAndServe(addr, s.Handler())
}
