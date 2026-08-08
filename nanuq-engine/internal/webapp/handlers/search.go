package handlers

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"slices"

	"nanuq-engine/internal/search"
	"nanuq-engine/internal/webapp/templates"
)

// SearchDeps carries the dependencies of the /search handler. It is wired
// by package webapp from the Server: the handler lives here instead of in
// server.go because package handlers must not import package webapp (that
// would create the webapp -> handlers -> webapp import cycle).
type SearchDeps struct {
	Svc     *search.SearchService
	Store   search.BangResolver
	Catalog search.EngineCatalog
	Formats []string
}

// outputFormats is the set of render formats the handler can produce,
// mirroring OUTPUT_FORMATS in webapp.py.
var outputFormats = map[string]bool{
	"html": true,
	"json": true,
	"csv":  true,
	"rss":  true,
}

// jsonResponse is the JSON body for format=json. Field order is
// significant (CA-003): all seven keys are always present.
type jsonResponse struct {
	Query               string              `json:"query"`
	Results             []map[string]any    `json:"results"`
	Answers             []map[string]any    `json:"answers"`
	Corrections         []string            `json:"corrections"`
	Infoboxes           []map[string]any    `json:"infoboxes"`
	Suggestions         []string            `json:"suggestions"`
	UnresponsiveEngines []map[string]string `json:"unresponsive_engines"`
}

// rssDecl is the static XML declaration for the RSS feed. It is written
// verbatim before the templated body, exactly like opensearchDecl in
// misc.go: html/template's HTML lexer would corrupt "<?xml" as text.
const rssDecl = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"

// rssItem is one <item> of the RSS 2.0 channel (title, link, description).
type rssItem struct {
	Title   string
	URL     string
	Content string
}

// rssBody is the template data for the RSS channel body.
type rssBody struct {
	Items []rssItem
}

// rssBodyTmpl renders the channel body only; the XML declaration comes
// from rssDecl. html/template autoescaping is safe here: & in titles and
// URLs becomes &amp;, which is valid XML as well as HTML.
var rssBodyTmpl = template.Must(template.New("rss").Parse(`<rss version="2.0">
<channel>
<title>searx</title>
{{range .Items}}
<item>
<title>{{.Title}}</title>
<link>{{.URL}}</link>
<description>{{.Content}}</description>
</item>
{{end}}
</channel>
</rss>
`))

// htmlResult is one rendered result row for the HTML results page.
type htmlResult struct {
	Title   string
	URL     string
	Content string
}

// resultsData is the template data for templates/results.html.
type resultsData struct {
	Query        string
	Results      []htmlResult
	Unresponsive []search.UnresponsiveEngine
}

// resultsTmpl is the embedded results page (REQ-017). Parse failures are
// build-time invariants: fail fast, like indexTmpl in misc.go.
var resultsTmpl = template.Must(template.ParseFS(templates.FS, "results.html"))

// SearchHandler returns the http.HandlerFunc for the /search route
// (REQ-017, TASK-012b). It ports webapp.py search() (L616-780) and
// webutils.get_json_response (L162-174) to Go.
//
// Flow: parse the form; reject formats the instance does not advertise
// (EC-008); parse the raw query; redirect on empty query or external bang;
// run the search service; then dispatch to json/csv/rss/html rendering.
func SearchHandler(deps SearchDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		q := r.FormValue("q")
		format := r.FormValue("format")
		if format == "" {
			format = "html"
		}

		// EC-008: when the instance advertises an explicit format list,
		// reject anything outside it. An empty Formats list allows all
		// formats (search.formats unset).
		if len(deps.Formats) > 0 && !slices.Contains(deps.Formats, format) {
			http.Error(w, "format not allowed", http.StatusForbidden)
			return
		}

		rawText := search.Parse(q, deps.Store, deps.Catalog)
		if rawText.GetQuery() == "" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		// External bang redirect (CA-008). GetBangURL resolves the bang
		// marker chr(2) to the substituted URL; an unknown bang is a bad
		// request (EC-002).
		if rawText.ExternalBang != "" {
			if deps.Store == nil {
				slog.Error("search: bang requested but bang store is nil", "bang", rawText.ExternalBang)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			url, ok := deps.Store.GetBangURL(rawText.ExternalBang, rawText.GetQuery())
			if !ok {
				http.Error(w, "unknown bang", http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, url, http.StatusFound)
			return
		}

		if deps.Svc == nil {
			slog.Error("search: search service is nil")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		rc := deps.Svc.Search(rawText)
		rc.Close(1.0, nil)

		// Redirect-to-first-result and similar container-level redirects.
		if url := rc.RedirectURL(); url != "" {
			http.Redirect(w, r, url, http.StatusFound)
			return
		}

		switch format {
		case "json":
			writeJSON(w, rawText.GetQuery(), rc)
		case "csv":
			writeCSV(w, rawText.GetQuery(), rc)
		case "rss":
			writeRSS(w, rc)
		case "html":
			writeHTML(w, rawText.GetQuery(), rc)
		default:
			// Advertised but not renderable: never serve HTML for a
			// different requested format.
			http.Error(w, "unsupported format", http.StatusBadRequest)
		}
	}
}

// writeJSON renders the seven-key JSON response (CA-003). Empty slices are
// built non-nil so they serialise as [] rather than null.
func writeJSON(w http.ResponseWriter, query string, rc *search.ResultContainer) {
	results := make([]map[string]any, 0, len(rc.GetOrderedResults()))
	for _, res := range rc.GetOrderedResults() {
		results = append(results, res.AsDict())
	}
	answers := make([]map[string]any, 0, len(rc.Answers()))
	for _, a := range rc.Answers() {
		answers = append(answers, a.AsDict())
	}
	infoboxes := make([]map[string]any, 0, len(rc.Infoboxes()))
	for _, ib := range rc.Infoboxes() {
		infoboxes = append(infoboxes, ib.AsDict())
	}
	unresponsive := make([]map[string]string, 0, len(rc.Unresponsive()))
	for _, u := range rc.Unresponsive() {
		unresponsive = append(unresponsive, map[string]string{"name": u.Name, "reason": u.Reason})
	}

	body := jsonResponse{
		Query:               query,
		Results:             results,
		Answers:             answers,
		Corrections:         rc.Corrections(),
		Infoboxes:           infoboxes,
		Suggestions:         rc.Suggestions(),
		UnresponsiveEngines: unresponsive,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("search: json encode", "err", err)
	}
}

// writeCSV renders an attachment CSV with title,url,content columns
// (mimetype and Filename header as in webapp.py get_csv_response).
func writeCSV(w http.ResponseWriter, query string, rc *search.ResultContainer) {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"title", "url", "content"})
	for _, res := range rc.GetOrderedResults() {
		_ = cw.Write([]string{res.Title, res.URL, res.Content})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		slog.Error("search: csv encode", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/csv")
	w.Header().Set("Content-Disposition", "attachment;Filename=searx_-_"+query+".csv")
	_, _ = w.Write(buf.Bytes())
}

// writeRSS renders the RSS 2.0 feed: static declaration first, then the
// templated channel body.
func writeRSS(w http.ResponseWriter, rc *search.ResultContainer) {
	items := make([]rssItem, 0, len(rc.GetOrderedResults()))
	for _, res := range rc.GetOrderedResults() {
		items = append(items, rssItem{Title: res.Title, URL: res.URL, Content: res.Content})
	}
	w.Header().Set("Content-Type", "text/xml")
	if _, err := io.WriteString(w, rssDecl); err != nil {
		return
	}
	if err := rssBodyTmpl.Execute(w, rssBody{Items: items}); err != nil {
		slog.Error("search: rss render", "err", err)
	}
}

// writeHTML renders templates/results.html with the query, the ordered
// results and the unresponsive-engine errors.
func writeHTML(w http.ResponseWriter, query string, rc *search.ResultContainer) {
	results := make([]htmlResult, 0, len(rc.GetOrderedResults()))
	for _, res := range rc.GetOrderedResults() {
		results = append(results, htmlResult{Title: res.Title, URL: res.URL, Content: res.Content})
	}
	data := resultsData{
		Query:        query,
		Results:      results,
		Unresponsive: rc.Unresponsive(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := resultsTmpl.Execute(w, data); err != nil {
		slog.Error("search: html render", "err", err)
	}
}
