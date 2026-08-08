package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"nanuq-searcher-mcp/internal/config"
	"nanuq-searcher-mcp/internal/crawl"
	"nanuq-searcher-mcp/internal/domain"
	"nanuq-searcher-mcp/internal/fetch"
	"nanuq-searcher-mcp/internal/markdown"
	"nanuq-searcher-mcp/internal/search"
)

// Tool names exposed by the nanuq MCP server.
const (
	ToolSearch = "nanuq_search" // REQ-002
	ToolFetch  = "nanuq_fetch"  // REQ-007
	ToolMap    = "nanuq_map"    // REQ-011
)

// searchTool returns the nanuq_search tool definition (REQ-002): free-text
// query with an optional category filter and a result cap.
func searchTool() mcpgo.Tool {
	return mcpgo.NewTool(ToolSearch,
		mcpgo.WithDescription(
			"Busca en el índice de nanuq y devuelve los resultados más "+
				"relevantes con su contexto extraído.",
		),
		mcpgo.WithString("query",
			mcpgo.Required(),
			mcpgo.Description("Consulta de búsqueda en texto libre."),
		),
		mcpgo.WithArray("categories",
			mcpgo.Description(
				"Filtra los resultados a estas categorías de motor (opcional).",
			),
			mcpgo.WithStringItems(),
		),
		mcpgo.WithInteger("max_results",
			mcpgo.Description("Número máximo de resultados a devolver (1-50)."),
			mcpgo.DefaultNumber(config.DefaultSearchMaxResults),
			mcpgo.Min(1),
			mcpgo.Max(50),
		),
	)
}

// fetchTool returns the nanuq_fetch tool definition (REQ-007/REQ-008):
// retrieves a URL and converts it to clean markdown.
func fetchTool() mcpgo.Tool {
	return mcpgo.NewTool(ToolFetch,
		mcpgo.WithDescription(
			"Obtiene una URL y la convierte a markdown limpio.",
		),
		mcpgo.WithString("url",
			mcpgo.Required(),
			mcpgo.Description("URL absoluta (http/https) a obtener."),
		),
		mcpgo.WithString("mode",
			mcpgo.Description(
				"Modo de extracción: readable extrae el artículo principal; "+
					"full devuelve el contenido completo.",
			),
			mcpgo.DefaultString("readable"),
			mcpgo.Enum("readable", "full"),
		),
		mcpgo.WithInteger("max_bytes",
			mcpgo.Description("Límite de bytes a descargar (64 KB - 10 MB)."),
			mcpgo.DefaultNumber(config.DefaultFetchMaxBytes),
			mcpgo.Min(64<<10),
			mcpgo.Max(10<<20),
		),
	)
}

// mapTool returns the nanuq_map tool definition (REQ-011): crawls a site
// respecting robots.txt and returns the site structure as a map.
func mapTool() mcpgo.Tool {
	return mcpgo.NewTool(ToolMap,
		mcpgo.WithDescription(
			"Explora un sitio web respetando robots.txt y devuelve el mapa "+
				"de la estructura del sitio.",
		),
		mcpgo.WithString("url",
			mcpgo.Required(),
			mcpgo.Description("URL raíz (http/https) desde la que mapear."),
		),
		mcpgo.WithInteger("max_pages",
			mcpgo.Description("Número máximo de páginas a explorar."),
			mcpgo.DefaultNumber(config.DefaultCrawlMaxPages),
		),
		mcpgo.WithInteger("max_depth",
			mcpgo.Description("Profundidad máxima de enlaces a seguir."),
			mcpgo.DefaultNumber(config.DefaultCrawlMaxDepth),
		),
		mcpgo.WithBoolean("same_host",
			mcpgo.Description("Limita el mapeo al mismo host que la URL raíz."),
			mcpgo.DefaultBool(true),
		),
		mcpgo.WithBoolean("respect_robots",
			mcpgo.Description("Respeta las reglas de robots.txt del sitio."),
			mcpgo.DefaultBool(true),
		),
		mcpgo.WithBoolean("include_content",
			mcpgo.Description("Incluye el contenido extraído de cada página."),
			mcpgo.DefaultBool(false),
		),
	)
}

// maxResults bounds for the nanuq_search tool (REQ-002, DSG-011). The
// upper bound mirrors the validation bound in internal/config.
const (
	searchMaxResultsMin = 1
	searchMaxResultsMax = 50
)

// maxBytes bounds for the nanuq_fetch tool (REQ-008, DSG-006). The range
// mirrors the validation bounds in internal/config.
const (
	fetchMaxBytesMin = 64 << 10
	fetchMaxBytesMax = 10 << 20
)

// maxPages bounds for the nanuq_map tool (REQ-011, DSG-008). The upper
// bound mirrors the validation bound in internal/config.
const (
	mapMaxPagesMin = 1
	mapMaxPagesMax = 10000
)

// maxDepth bounds for the nanuq_map tool (REQ-011, DSG-008). The upper
// bound mirrors the validation bound in internal/config.
const (
	mapMaxDepthMin = 1
	mapMaxDepthMax = 10
)

// handleSearch implements the nanuq_search tool (REQ-002) on top of the
// search.Adapter. Argument validation failures are reported as protocol
// errors (isError); domain failures from the engine are rendered as
// markdown without isError (DSG-011).
func (s *Server) handleSearch(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	if args == nil {
		args = map[string]any{}
	}

	// query: required string (REQ-002).
	query, ok := args["query"].(string)
	if !ok {
		return mcpgo.NewToolResultError(
			"`nanuq_search`: el argumento `query` debe ser una cadena de texto",
		), nil
	}

	// categories: optional list of strings.
	var categories []string
	if raw, present := args["categories"]; present && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return mcpgo.NewToolResultError(
				"`nanuq_search`: el argumento `categories` debe ser una lista de cadenas",
			), nil
		}
		categories = make([]string, 0, len(list))
		for _, item := range list {
			cat, ok := item.(string)
			if !ok {
				return mcpgo.NewToolResultError(
					"`nanuq_search`: `categories` solo admite cadenas",
				), nil
			}
			categories = append(categories, cat)
		}
	}

	// max_results: optional number; must be an integer in [1, 50] (REQ-002,
	// DSG-011). mcp-go decodes JSON numbers as float64.
	maxResults := config.DefaultSearchMaxResults
	if raw, present := args["max_results"]; present && raw != nil {
		f, ok := raw.(float64)
		if !ok {
			return mcpgo.NewToolResultError(
				"`nanuq_search`: el argumento `max_results` debe ser un número entero",
			), nil
		}
		mr := int(f)
		if mr < searchMaxResultsMin || mr > searchMaxResultsMax {
			return mcpgo.NewToolResultError(fmt.Sprintf(
				"`nanuq_search`: `max_results` debe estar entre %d y %d (recibido %d)",
				searchMaxResultsMin, searchMaxResultsMax, mr,
			)), nil
		}
		maxResults = mr
	}

	if s.svc == nil {
		return mcpgo.NewToolResultError(
			"`nanuq_search`: el servicio de búsqueda no está configurado",
		), nil
	}

	result, err := search.NewAdapter(s.svc).Search(ctx, query, categories, maxResults)
	if err != nil {
		// DSG-011: domain errors are markdown, not protocol errors.
		return mcpgo.NewToolResultText(fmt.Sprintf(
			"## ⚠️ Error en la búsqueda\n\n%s", err,
		)), nil
	}
	return mcpgo.NewToolResultText(markdown.RenderSearch(*result)), nil
}

// handleFetch implements the nanuq_fetch tool (REQ-007/REQ-008) on top of
// internal/fetch and internal/markdown. Argument validation failures are
// reported as protocol errors (isError); domain failures (network,
// non-HTML response, HTTP status, conversion) are rendered as markdown
// without isError, mirroring DSG-011.
func (s *Server) handleFetch(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	if args == nil {
		args = map[string]any{}
	}

	// url: required string (REQ-007). The client re-parses and validates
	// the URL; here we only reject empty/non-string values.
	rawURL, ok := args["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return mcpgo.NewToolResultError(
			"`nanuq_fetch`: el argumento `url` es obligatorio y debe ser una URL http(s) no vacía",
		), nil
	}

	// mode: optional; "" defaults to "readable"; only "readable"|"full" are
	// valid (REQ-008).
	mode := "readable"
	if raw, present := args["mode"]; present && raw != nil {
		m, ok := raw.(string)
		if !ok {
			return mcpgo.NewToolResultError(
				"`nanuq_fetch`: el argumento `mode` debe ser una cadena",
			), nil
		}
		switch m {
		case "", "readable":
			mode = "readable"
		case "full":
			mode = "full"
		default:
			return mcpgo.NewToolResultError(fmt.Sprintf(
				"`nanuq_fetch`: `mode` debe ser \"readable\" o \"full\" (recibido %q)", m,
			)), nil
		}
	}

	// max_bytes: optional number; must be an integer in [64 KB, 10 MB]
	// (REQ-008). mcp-go decodes JSON numbers as float64.
	maxBytes := config.DefaultFetchMaxBytes
	if raw, present := args["max_bytes"]; present && raw != nil {
		f, ok := raw.(float64)
		if !ok {
			return mcpgo.NewToolResultError(
				"`nanuq_fetch`: el argumento `max_bytes` debe ser un número entero",
			), nil
		}
		mb := int(f)
		if mb < fetchMaxBytesMin || mb > fetchMaxBytesMax {
			return mcpgo.NewToolResultError(fmt.Sprintf(
				"`nanuq_fetch`: `max_bytes` debe estar entre %d y %d (recibido %d)",
				fetchMaxBytesMin, fetchMaxBytesMax, mb,
			)), nil
		}
		maxBytes = mb
	}

	client, err := fetchClientFromConfig(s.cfg)
	if err != nil {
		return mcpgo.NewToolResultText(fmt.Sprintf(
			"## ⚠️ Error al obtener la página\n\n%s", err,
		)), nil
	}

	// Fetch: network errors, non-HTML responses and HTTP error statuses
	// come back as descriptive errors (REQ-010) and are rendered as
	// markdown without isError.
	resp, err := client.Get(ctx, rawURL)
	if err != nil {
		return mcpgo.NewToolResultText(fmt.Sprintf(
			"## ⚠️ Error al obtener la página\n\n%s", err,
		)), nil
	}

	// Extraction: readable mode uses readability, falling back to the full
	// page when nothing extractable is found (REQ-008).
	content := string(resp.Body)
	title := ""
	// ContentHTML from Extract is already UTF-8 (Extract pre-decodes the raw
	// body via the WHATWG algorithm, TASK-014), so re-decoding it with the
	// detected charset would double-decode non-ASCII text (mojibake). The raw
	// body keeps resp.Charset for ConvertHTML to decode.
	charsetLabel := resp.Charset
	if mode == "readable" {
		pageURL, perr := url.Parse(resp.FinalURL)
		if perr != nil {
			return mcpgo.NewToolResultText(fmt.Sprintf(
				"## ⚠️ Error al obtener la página\n\nURL no válida: %v", perr,
			)), nil
		}
		if ext, _ := fetch.Extract(resp.Body, pageURL); ext.OK {
			content = ext.ContentHTML
			title = ext.Title
			charsetLabel = "utf-8"
		}
	}

	// Conversion: HTML to GFM markdown, charset-decoded and truncated to
	// max_bytes (the converter appends its own truncation note).
	md, err := markdown.ConvertHTML([]byte(content), charsetLabel, maxBytes)
	if err != nil {
		return mcpgo.NewToolResultText(fmt.Sprintf(
			"## ⚠️ Error al convertir la página\n\n%s", err,
		)), nil
	}

	if title == "" {
		title = resp.FinalURL
	}
	out := fmt.Sprintf("# %s\n\nURL: %s\n\n%s", title, resp.FinalURL, md)
	return mcpgo.NewToolResultText(out), nil
}

// fetchClientFromConfig builds a *fetch.Client from the MCP config, falling
// back to the fetch package defaults (DSG-010) when cfg is nil (server
// created without an MCP config). The client is built per call: it is cheap
// to construct (validation + http.Client wiring) and keeps the Server free
// of mutable state.
func fetchClientFromConfig(cfg *config.Config) (*fetch.Client, error) {
	if cfg == nil {
		return fetch.New(fetch.Config{})
	}
	return fetch.New(fetch.Config{
		TimeoutSec:   cfg.Fetch.TimeoutSec,
		MaxBytes:     cfg.Fetch.MaxBytes,
		MaxRedirects: cfg.Fetch.MaxRedirects,
		UserAgent:    cfg.UserAgent,
	})
}

// handleMap implements the nanuq_map tool (REQ-011/REQ-013) on top of
// internal/crawl and internal/markdown. Argument validation failures are
// reported as protocol errors (isError); domain failures (invalid root URL,
// crawl errors) are rendered as markdown without isError, mirroring DSG-011.
func (s *Server) handleMap(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	if args == nil {
		args = map[string]any{}
	}

	// url: required string (REQ-011).
	rawURL, ok := args["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return mcpgo.NewToolResultError(
			"`nanuq_map`: el argumento `url` es obligatorio y debe ser una URL http(s) no vacía",
		), nil
	}

	// max_pages: optional number; must be an integer in [1, 10000] (REQ-011).
	maxPages := config.DefaultCrawlMaxPages
	if raw, present := args["max_pages"]; present && raw != nil {
		f, ok := raw.(float64)
		if !ok {
			return mcpgo.NewToolResultError(
				"`nanuq_map`: el argumento `max_pages` debe ser un número entero",
			), nil
		}
		mp := int(f)
		if mp < mapMaxPagesMin || mp > mapMaxPagesMax {
			return mcpgo.NewToolResultError(fmt.Sprintf(
				"`nanuq_map`: `max_pages` debe estar entre %d y %d (recibido %d)",
				mapMaxPagesMin, mapMaxPagesMax, mp,
			)), nil
		}
		maxPages = mp
	}

	// max_depth: optional number; must be an integer in [1, 10] (REQ-013).
	maxDepth := config.DefaultCrawlMaxDepth
	if raw, present := args["max_depth"]; present && raw != nil {
		f, ok := raw.(float64)
		if !ok {
			return mcpgo.NewToolResultError(
				"`nanuq_map`: el argumento `max_depth` debe ser un número entero",
			), nil
		}
		md := int(f)
		if md < mapMaxDepthMin || md > mapMaxDepthMax {
			return mcpgo.NewToolResultError(fmt.Sprintf(
				"`nanuq_map`: `max_depth` debe estar entre %d y %d (recibido %d)",
				mapMaxDepthMin, mapMaxDepthMax, md,
			)), nil
		}
		maxDepth = md
	}

	// same_host / respect_robots / include_content: optional booleans
	// (REQ-011/REQ-013). Zero values are false by default; the tool schema
	// defaults them to true, so omitting them yields true (per REQ-011).
	sameHost, err := mapArgBool(args, "same_host", true)
	if err != nil {
		return mcpgo.NewToolResultError(
			"`nanuq_map`: el argumento `same_host` debe ser un booleano",
		), nil
	}
	respectRobots, err := mapArgBool(args, "respect_robots", true)
	if err != nil {
		return mcpgo.NewToolResultError(
			"`nanuq_map`: el argumento `respect_robots` debe ser un booleano",
		), nil
	}
	includeContent, err := mapArgBool(args, "include_content", false)
	if err != nil {
		return mcpgo.NewToolResultError(
			"`nanuq_map`: el argumento `include_content` debe ser un booleano",
		), nil
	}

	// The crawl layer normalizes the root URL again, but rejecting an
	// unnormalizable URL here keeps it an input-validation error (isError)
	// instead of a domain error (DSG-011).
	if _, err := crawl.NormalizeURL(rawURL); err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf(
			"`nanuq_map`: URL raíz no válida (%v)", err,
		)), nil
	}

	// CrawlConfig wiring: max_pages and max_depth come from the tool args;
	// workers, timeout and user agent come from the MCP config (s.cfg) when
	// present, falling back to the code-first defaults in internal/config
	// (DSG-010). The crawler's withDefaults applies the same fallbacks, so
	// a zero-valued config behaves identically.
	workers := config.DefaultCrawlWorkers
	timeoutSec := config.DefaultCrawlTimeoutSec
	userAgent := config.DefaultUserAgent
	if s.cfg != nil {
		if s.cfg.Crawl.Workers > 0 {
			workers = s.cfg.Crawl.Workers
		}
		if s.cfg.Crawl.TimeoutSec > 0 {
			timeoutSec = s.cfg.Crawl.TimeoutSec
		}
		if s.cfg.UserAgent != "" {
			userAgent = s.cfg.UserAgent
		}
	}
	cc := crawl.CrawlConfig{
		Workers:       workers,
		MaxPages:      maxPages,
		MaxDepth:      maxDepth,
		SameHost:      sameHost,
		RespectRobots: respectRobots,
		TimeoutSec:    timeoutSec,
		UserAgent:     userAgent,
	}

	sm := crawl.Crawl(ctx, rawURL, cc, s.log)
	if sm == nil {
		// Defensive: Crawl never returns nil, but guard anyway.
		return mcpgo.NewToolResultText(
			"## ⚠️ Error al mapear el sitio\n\nel crawler devolvió un resultado vacío",
		), nil
	}

	if includeContent {
		enrichMapContent(ctx, s.cfg, sm)
		return mcpgo.NewToolResultText(markdown.RenderMapContent(*sm)), nil
	}
	return mcpgo.NewToolResultText(markdown.RenderMap(*sm)), nil
}

// mapArgBool reads a boolean argument from the args map, returning def when
// the key is absent or nil (mcp-go decodes JSON booleans as bool). A present
// non-boolean value yields an error.
func mapArgBool(args map[string]any, key string, def bool) (bool, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return def, nil
	}
	v, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("argumento %q debe ser un booleano", key)
	}
	return v, nil
}

// enrichMapContent re-fetches every page of the site map (REQ-013) and fills
// Page.Content with markdown converted from the page body. The fetch client
// is built per call (fetchClientFromConfig, DSG-010) so the Server keeps no
// mutable state. Failures on a single page are appended to Page.Errors
// instead of aborting the whole map; the per-page conversion is capped at
// maxBytes (cfg.Fetch.MaxBytes, default 2 MiB) so the output stays bounded.
func enrichMapContent(ctx context.Context, cfg *config.Config, sm *domain.SiteMap) {
	client, err := fetchClientFromConfig(cfg)
	if err != nil {
		for i := range sm.Pages {
			sm.Pages[i].Errors = append(sm.Pages[i].Errors, fmt.Sprintf("content: %v", err))
		}
		return
	}
	maxBytes := int(config.DefaultFetchMaxBytes)
	if cfg != nil && cfg.Fetch.MaxBytes > 0 {
		maxBytes = int(cfg.Fetch.MaxBytes)
	}
	for i := range sm.Pages {
		if ctx.Err() != nil {
			return
		}
		page := &sm.Pages[i]
		resp, err := client.Get(ctx, page.URL)
		if err != nil {
			page.Errors = append(page.Errors, fmt.Sprintf("content: %v", err))
			continue
		}
		md, err := contentMarkdown(resp, maxBytes)
		if err != nil {
			page.Errors = append(page.Errors, fmt.Sprintf("content: %v", err))
			continue
		}
		page.Content = md
	}
}

// contentMarkdown converts a fetch.Response body to markdown for the map
// content enrichment. It converts the RAW body with the detected charset,
// deliberately NOT fetch.Extract's ContentHTML: Extract returns a UTF-8 Go
// string (go-shiori/dom.Parse re-encodes the bytes with an internal chardet
// detector, which mis-detects short documents and double-encodes them), so
// feeding that string back through ConvertHTML with the page's charset
// double-decodes non-ASCII text (observed as mojibake on short pages).
// Converting the raw body with its detected charset is always correct and
// mirrors the handleFetch full-mode fallback (REQ-008).
func contentMarkdown(resp *fetch.Response, maxBytes int) (string, error) {
	return markdown.ConvertHTML(resp.Body, resp.Charset, maxBytes)
}
