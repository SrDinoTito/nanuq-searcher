// This file is a faithful Go port of SearXNG's searx/engines/google.py (web
// results). Documented deviations vs. the Python module are annotated inline
// and summarised in the RESULT report of this task.
package engines

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// googleTimeRangeMap ports google.py's time_range_dict (L62): the tbs time
// range codes for the "time_range" request parameter.
var googleTimeRangeMap = map[string]string{
	"day":   "d",
	"week":  "w",
	"month": "m",
	"year":  "y",
}

// googleFilterMap ports google.py's filter_mapping (L65): the safe query
// parameter values for safe search off / moderate / strict. Unlike Bing,
// Google only emits the safe parameter when safe search is active
// (google.py L339-340: if params["safesearch"]: ...).
var googleFilterMap = map[int]string{
	0: "off",
	1: "medium",
	2: "high",
}

// googleEngine implements engine.Engine for Google web results (google.py).
type googleEngine struct {
	cfg *config.EngineConfig

	// baseURL ports the subdomain part of google.py's request URL
	// (L310-313). SearXNG resolves the subdomain from the engine's traits
	// (traits.custom["supported_domains"], L187); those traits are not
	// ported, so the subdomain is taken from cfg.Overrides["base_url"] with
	// "https://www.google.com" as default (documented deviation).
	baseURL string

	// defCats are the module's categories (google.py L51); they are used
	// when the YAML entry does not declare categories of its own.
	defCats []string
}

// NewGoogleEngine builds one Google (web) engine per YAML entry.
func NewGoogleEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: google engine: nil config", engine.ErrInvalidConfig)
	}
	return &googleEngine{
		cfg:     cfg,
		baseURL: overrideStringDef(cfg.Overrides, "base_url", "https://www.google.com"),
		defCats: []string{"general", "web"},
	}, nil
}

// Name returns the engine's configured name (YAML entry name).
func (e *googleEngine) Name() string { return e.cfg.Name }

// Shortcut returns the configured shortcut.
func (e *googleEngine) Shortcut() string { return e.cfg.Shortcut }

// Categories returns the configured categories, falling back to the module's
// default categories when the YAML entry declares none.
func (e *googleEngine) Categories() []string {
	if len(e.cfg.Categories) > 0 {
		return e.cfg.Categories
	}
	return e.defCats
}

// NeedsInit reports that no per-engine init is required.
func (e *googleEngine) NeedsInit() bool { return false }

// Setup is a no-op for the Google engine (all config comes from Overrides).
func (e *googleEngine) Setup(_ context.Context, _ *config.EngineConfig) error { return nil }

// Init is a no-op. google.py's init fetches the engine traits (L459-535
// fetch_traits); that table is out of scope for this port — the search
// processor supplies the locale directly via params.Language (see
// googleLocale).
func (e *googleEngine) Init(_ context.Context) error { return nil }

// googleLocale resolves the hl / lr / cr query parameters for a request.
// Port of google.py's get_google_info (L101-277) with the engine traits
// replaced by params.Language (documented deviation): in google.py the
// language comes from eng_traits.get_language(searxng_locale, "lang_en")
// and the region from eng_traits.get_region(searxng_locale,
// traits.all_locale). "all" and "clear" are the no-locale sentinels, for
// which google.py keeps lr and cr empty and hl defaults to the en code
// (eng_lang default "lang_en").
func googleLocale(params *engine.RequestParams) (hl, lr, cr string) {
	lang := params.Language
	switch lang {
	case "", "all", "clear":
		return "en", "", ""
	}
	// google.py L173: lang_code = eng_lang.split("_")[-1]; with the traits
	// approximation eng_lang is "lang_<lang>", so lang_code == lang.
	hl = lang
	// google.py L215-217: lr = eng_lang ("" for the "all" locale).
	lr = "lang_" + lang
	// google.py L226-228: cr = "country" + region when the locale carries a
	// region part (the part after the last "-").
	if i := strings.LastIndexByte(lang, '-'); i > 0 {
		cr = "country" + strings.ToUpper(lang[i+1:])
	}
	return hl, lr, cr
}

// errGoogleCaptcha stands in for SearXNG's SearxEngineCaptchaException
// (google.py L294-300). engine.Errors has no captcha type, so a local
// sentinel is returned (documented deviation: the package-level error
// taxonomy is not extended).
var errGoogleCaptcha = errors.New("google engine: captcha challenge detected")

// googleDetectSorry ports google.py's detect_google_sorry (L280-300): Google
// bot protection is detected when the final URL is on the sorry host/path,
// the response is a 302 redirect, or a very short body mentions "/sorry/".
// The Python raises on any of the three conditions; the port returns the
// sentinel error instead.
func googleDetectSorry(resp *http.Response, bodyLen int, body string) error {
	// google.py L293: resp.url.host == "sorry.google.com" or
	// resp.url.path.startswith("/sorry"). In Go the final URL lives on
	// resp.Request.URL, which is nil for synthetic test responses.
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		u := resp.Request.URL
		if strings.EqualFold(u.Hostname(), "sorry.google.com") || strings.HasPrefix(u.Path, "/sorry") {
			return errGoogleCaptcha
		}
	}
	// google.py L296-297: a 302 is the classic captcha handoff.
	if resp != nil && resp.StatusCode == http.StatusFound {
		return errGoogleCaptcha
	}
	// google.py L299-300: short bodies mentioning "/sorry/".
	if bodyLen < 2000 && strings.Contains(body, "/sorry/") {
		return errGoogleCaptcha
	}
	return nil
}

// googleDataImageRE ports google.py's RE_DATA_IMAGE (L349): it matches the
// data:image src and the following id (dimg/pimg/tsuid) of an image found in
// the page's JavaScript payload.
var googleDataImageRE = regexp.MustCompile(`(data:image[^']*?)'[^']*?'((?:dimg|pimg|tsuid)[^']*)`)

// googleParseURLImages ports google.py's parse_url_images (L352-358): it
// maps every image id to its data:image URL, decoded with Python's
// 'unicode-escape' codec (see pythonUnicodeEscape).
func googleParseURLImages(text string) map[string]string {
	dataImageMap := make(map[string]string)
	for _, m := range googleDataImageRE.FindAllStringSubmatch(text, -1) {
		if len(m) < 3 {
			continue
		}
		// google.py L356: group 1 is the image URL, group 2 the img id.
		dataImageMap[m[2]] = pythonUnicodeEscape(m[1])
	}
	return dataImageMap
}

// pythonUnicodeEscape ports Python's 'unicode-escape' text codec used by
// google.py L356: \uXXXX, \xXX and the common escapes \n, \t and \r are
// decoded; any malformed sequence is kept verbatim (defensive, EC-011).
func pythonUnicodeEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		switch s[i+1] {
		case 'u':
			if i+6 <= len(s) {
				if r, err := strconv.ParseUint(s[i+2:i+6], 16, 16); err == nil {
					b.WriteRune(rune(r))
					i += 5
					continue
				}
			}
			b.WriteByte(c)
		case 'x':
			if i+4 <= len(s) {
				if r, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
					b.WriteRune(rune(r))
					i += 3
					continue
				}
			}
			b.WriteByte(c)
		case 'n':
			b.WriteByte('\n')
			i++
		case 't':
			b.WriteByte('\t')
			i++
		case 'r':
			b.WriteByte('\r')
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// setEngineCookie mirrors the Python params["cookies"][name] = value dict
// assignment (e.g. brave.py L221-230): a cookie of the same name is replaced,
// otherwise the cookie is appended.
func setEngineCookie(params *engine.RequestParams, name, value string) {
	for i := range params.Cookies {
		if params.Cookies[i].Name == name {
			params.Cookies[i].Value = value
			return
		}
	}
	params.Cookies = append(params.Cookies, &http.Cookie{Name: name, Value: value})
}

// Request mutates params to build the Google search request. Port of
// google.py request() (L303-344). Note that Google uses a GET request with
// the query in the URL — the task text mentioned POST with form data, but
// the Python module is the source of truth and uses GET (documented
// deviation).
func (e *googleEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("google engine: nil request params")
	}

	// google.py L306: start = (params["pageno"] - 1) * 10.
	pageno := params.Pageno
	if pageno < 1 {
		pageno = 1
	}

	// google.py L307: google_info = get_google_info(params, traits).
	hl, lr, cr := googleLocale(params)

	// google.py L310-335: query_params = {"q": query,
	// **google_info["params"], "filter": "0", "start": start}.
	v := url.Values{}
	v.Set("q", query)
	v.Set("hl", hl)
	v.Set("lr", lr)
	v.Set("cr", cr)
	v.Set("ie", "utf8")
	v.Set("oe", "utf8")
	v.Set("filter", "0")
	v.Set("start", strconv.Itoa((pageno-1)*10))

	// google.py L337-338: time range → &tbs=qdr:<code>.
	if code, ok := googleTimeRangeMap[params.TimeRange]; ok {
		v.Set("tbs", "qdr:"+code)
	}
	// google.py L339-340: safe search (0 is falsy in Python, so the
	// parameter is only emitted when safe search is active).
	if params.SafeSearch > 0 {
		if safe, ok := googleFilterMap[params.SafeSearch]; ok {
			v.Set("safe", safe)
		}
	}

	// google.py L341: params["url"] = query_url.
	params.URL = e.baseURL + "/search?" + v.Encode()
	params.Method = http.MethodGet

	// google.py L343-344: params["cookies"] = google_info["cookies"]
	// replaces the whole cookie set with the CONSENT cookie, and the
	// Accept header from get_google_info is merged in.
	params.Cookies = []*http.Cookie{{Name: "CONSENT", Value: "YES+"}}
	if params.Headers == nil {
		params.Headers = make(http.Header)
	}
	params.Headers.Set("Accept", "*/*")
	return nil
}

// Response parses the Google HTML and extracts the organic results plus any
// suggestions. Port of google.py response() (L361-427).
func (e *googleEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("google engine: nil http response")
	}

	// google.py L364: detect_google_sorry(resp) raises on bot protection
	// before anything else runs. The body is read once so it can be
	// inspected by the detector and then parsed.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("google engine %q: read response body: %w", e.cfg.Name, err)
	}
	if err := googleDetectSorry(resp, len(body), string(body)); err != nil {
		return nil, err
	}

	// google.py L365: data_image_map = parse_url_images(resp.text).
	dataImageMap := googleParseURLImages(string(body))

	// google.py L370: dom = html.fromstring(resp.text).
	doc, err := htmlquery.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("google engine %q: parse response body: %w", e.cfg.Name, err)
	}

	results := []*result.RawResult{}

	// google.py L373: for result in eval_xpath_list(dom,
	// '//a[@data-ved and not(@class)]').
	items, err := htmlquery.QueryAll(doc, `//a[@data-ved and not(@class)]`)
	if err != nil {
		return nil, fmt.Errorf("google engine %q: evaluate result xpath: %w", e.cfg.Name, err)
	}
	for _, item := range items {
		// google.py L376-419: a broad try/except drops malformed items; the
		// defensive query helpers below achieve the same by returning nil.
		//
		// google.py L377-382: title_tag = first './/div[@style]'.
		titleTag := bingQuery(item, `.//div[@style]`)
		if titleTag == nil {
			continue
		}
		title := extractText([]*html.Node{titleTag})

		// google.py L384-390: raw_url = result.get("href"); skip when
		// missing.
		rawURL := htmlquery.SelectAttr(item, "href")
		if rawURL == "" {
			continue
		}
		// google.py L392-395: google redirector links are unquoted after
		// stripping the "&sa=U" tracking parameter. Python's unquote does
		// not touch '+' and neither does url.PathUnescape.
		resultURL := rawURL
		if strings.HasPrefix(rawURL, "/url?q=") {
			if decoded, derr := url.PathUnescape(strings.SplitN(rawURL[7:], "&sa=U", 2)[0]); derr == nil {
				resultURL = decoded
			}
			// Malformed escapes keep the raw URL (defensive, EC-011).
		}

		// google.py L397-401: content_nodes = '../..//div[contains(@class,
		// "ilUpNd H66NU aSRlid")]'; the script children are removed so the
		// snippet text stays clean.
		contentNodes := bingQueryAll(item, `../..//div[contains(@class, "ilUpNd H66NU aSRlid")]`)
		for _, node := range contentNodes {
			for _, script := range bingQueryAll(node, `.//script`) {
				if script.Parent != nil {
					script.Parent.RemoveChild(script)
				}
			}
		}
		// google.py L402: content = extract_text(content_nodes[0]). On an
		// empty list the Python indexing raises IndexError, which the broad
		// except turns into a skipped result; the port skips explicitly.
		if len(contentNodes) == 0 {
			continue
		}
		content := extractText([]*html.Node{contentNodes[0]})

		// google.py L404-413: thumbnail from the first img; data:image srcs
		// are resolved through the data_image_map by img id.
		thumbnail := ""
		if img := bingQuery(item, `.//img`); img != nil {
			thumbnail = htmlquery.SelectAttr(img, "src")
			if strings.HasPrefix(thumbnail, "data:image") {
				if imgID := htmlquery.SelectAttr(img, "id"); imgID != "" {
					if mapped, ok := dataImageMap[imgID]; ok {
						thumbnail = mapped
					} else {
						thumbnail = ""
					}
				}
			}
		}

		// google.py L415: content or ''.
		results = append(results, result.NewMain(&result.MainResult{
			URL:       resultURL,
			Title:     title,
			Content:   content,
			Thumbnail: thumbnail,
		}))
	}

	// google.py L422-424: suggestion_xpath. The Python appends every match,
	// empty or not; the port skips empty texts (defensive, EC-011, matching
	// the other engine ports of this package).
	suggestions, err := htmlquery.QueryAll(doc, `//div[contains(@class, "gGQDvd iIWm4b")]//a`)
	if err == nil {
		for _, s := range suggestions {
			if text := extractText([]*html.Node{s}); text != "" {
				results = append(results, result.NewSuggestion(text))
			}
		}
	}

	slog.Debug("google engine: extracted results", "engine", e.cfg.Name, "count", len(results))
	return results, nil
}
