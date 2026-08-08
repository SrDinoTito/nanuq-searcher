// Package domain holds the clean data types shared across the MCP layers
// (DSG-004). These types are deliberately small and agent-friendly: they
// never carry the SearXNG junk fields (parsed_url, template, priority,
// thumbnail, img_src, positions — REQ-003).
package domain

// SearchHit is the clean projection of a single search result (REQ-003).
// It is produced from a SearXNG-style dict by internal/search.Project.
type SearchHit struct {
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	URL      string   `json:"url"`
	Engines  []string `json:"engines"`
	Score    float64  `json:"score"`
	Category string   `json:"category"`
}

// SearchResult is the clean outcome of a search for one query (REQ-002).
// It feeds the markdown renderer (REQ-004..006).
type SearchResult struct {
	Query        string      `json:"query"`
	Hits         []SearchHit `json:"hits"`
	Unresponsive []string    `json:"unresponsive,omitempty"`
	RedirectURL  string      `json:"redirect_url,omitempty"`
}

// Page is one crawled page of a site map (DSG-004, REQ-011/012).
// Errors holds non-fatal errors encountered while fetching the page.
type Page struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Depth    int       `json:"depth"`
	Headings []Heading `json:"headings,omitempty"`
	Content  string    `json:"content,omitempty"`
	Errors   []string  `json:"errors,omitempty"`
}

// Heading is a page heading at level 1..6 (used for the site map outline,
// REQ-014).
type Heading struct {
	Level int    `json:"level"` // 1..6
	Text  string `json:"text"`
}

// SiteMap is the clean outcome of a crawl (DSG-004, REQ-011/012/015).
// Visited counts the pages actually visited; Cancelled is set when the
// crawl was stopped by context cancellation; HostErrors records per-host
// failures (e.g. persistent 429/5xx, EC-006).
type SiteMap struct {
	RootURL    string            `json:"root_url"`
	Pages      []Page            `json:"pages"`
	Visited    int               `json:"visited"`
	Cancelled  bool              `json:"cancelled,omitempty"`
	HostErrors map[string]string `json:"host_errors,omitempty"`
}
