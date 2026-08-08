// Package config provides typed YAML loading for nanuq-server settings.
//
// Design follows DSG-007 (defaults code-first, YAML only overrides them) and
// REQ-NF-004 (statically typed configuration, no reflection/metaprogramming).
package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Config is the root of the typed settings tree. Sections not modeled here
// (e.g. plugins, categories_as_tabs) are silently ignored during FASE A.
type Config struct {
	General     General        `yaml:"general"`
	Brand       Brand          `yaml:"brand"`
	Search      Search         `yaml:"search"`
	Server      Server         `yaml:"server"`
	Valkey      Valkey         `yaml:"valkey"`
	UI          UI             `yaml:"ui"`
	Preferences Preferences    `yaml:"preferences"`
	Outgoing    Outgoing       `yaml:"outgoing"`
	Engines     []EngineConfig `yaml:"engines"`
}

// General holds instance-wide settings (REQ-021: general section).
type General struct {
	Debug            bool       `yaml:"debug"`
	InstanceName     string     `yaml:"instance_name"`
	PrivacypolicyURL FlexString `yaml:"privacypolicy_url"`
	DonationURL      FlexString `yaml:"donation_url"`
	ContactURL       FlexString `yaml:"contact_url"`
	EnableMetrics    bool       `yaml:"enable_metrics"`
}

// Brand holds instance branding links.
type Brand struct {
	DocsURL         string `yaml:"docs_url"`
	PublicInstances string `yaml:"public_instances"`
	WikiURL         string `yaml:"wiki_url"`
	IssueURL        string `yaml:"issue_url"`
}

// Search holds search-related settings (REQ-021: search section).
type Search struct {
	SafeSearch       int            `yaml:"safe_search"`
	Autocomplete     string         `yaml:"autocomplete"`
	BanTimeOnFail    int            `yaml:"ban_time_on_fail"`
	MaxBanTimeOnFail int            `yaml:"max_ban_time_on_fail"`
	SuspendedTimes   map[string]int `yaml:"suspended_times"`
	Formats          StringList     `yaml:"formats"`
}

// Server holds HTTP server settings.
type Server struct {
	Port           int        `yaml:"port"`
	BindAddress    string     `yaml:"bind_address"`
	BaseURL        FlexString `yaml:"base_url"`
	Limiter        bool       `yaml:"limiter"`
	PublicInstance bool       `yaml:"public_instance"`
	SecretKey      string     `yaml:"secret_key"`
	ImageProxy     bool       `yaml:"image_proxy"`
}

// Valkey holds valkey (redis-compatible) connection settings.
type Valkey struct {
	URL FlexString `yaml:"url"`
}

// UI holds webapp appearance settings (completed in TASK-022).
type UI struct {
	StaticPath             string         `yaml:"static_path"`
	TemplatesPath          string         `yaml:"templates_path"`
	DefaultTheme           string         `yaml:"default_theme"`
	DefaultLocale          string         `yaml:"default_locale"`
	Hotkeys                string         `yaml:"hotkeys"`
	URLFormatting          string         `yaml:"url_formatting"`
	QueryInTitle           bool           `yaml:"query_in_title"`
	CenterAlignment        bool           `yaml:"center_alignment"`
	SearchOnCategorySelect bool           `yaml:"search_on_category_select"`
	ThemeArgs              map[string]any `yaml:"theme_args"`
}

// Preferences holds user preference locks.
type Preferences struct {
	Lock []string `yaml:"lock"`
}

// Outgoing holds outbound HTTP client settings.
type Outgoing struct {
	RequestTimeout    float64             `yaml:"request_timeout"`
	MaxRequestTimeout float64             `yaml:"max_request_timeout"`
	PoolConnections   int                 `yaml:"pool_connections"`
	PoolMaxsize       int                 `yaml:"pool_maxsize"`
	EnableHTTP2       bool                `yaml:"enable_http2"`
	Proxies           map[string][]string `yaml:"proxies"`
	UsingTorProxy     bool                `yaml:"using_tor_proxy"`
	ExtraProxyTimeout float64             `yaml:"extra_proxy_timeout"`
	SourceIPs         []string            `yaml:"source_ips"`
	Verify            FlexString          `yaml:"verify"`
}

// EngineConfig describes a single search engine entry. Engine-specific keys
// that are not modeled above (search_url, results_xpath, language, xpath, ...)
// are collected into Overrides (pattern DSG-003 / REQ-004, equivalent to
// SearXNG's update_engine_attributes).
type EngineConfig struct {
	Name       string         `yaml:"name"`
	Engine     string         `yaml:"engine"`
	Shortcut   string         `yaml:"shortcut"`
	Categories StringList     `yaml:"categories"`
	Timeout    float64        `yaml:"timeout"`
	Weight     float64        `yaml:"weight"`
	Disabled   bool           `yaml:"disabled"`
	Inactive   bool           `yaml:"inactive"`
	Overrides  map[string]any `yaml:",inline"`
}

// FlexString is a string that accepts either a YAML string or the boolean
// false. SearXNG settings use `base_url: false`, `valkey.url: false`, etc. to
// mean "not set" (REQ-021). Implemented with an UnmarshalYAML hook — no
// reflection (REQ-NF-004).
type FlexString string

// UnmarshalYAML implements yaml.Unmarshaler for FlexString.
func (s *FlexString) UnmarshalYAML(node *yaml.Node) error {
	switch node.Tag {
	case "!!str":
		*s = FlexString(node.Value)
		return nil
	case "!!bool":
		if node.Value == "false" {
			*s = ""
			return nil
		}
		return fmt.Errorf("config: FlexString: boolean %q is not supported, use a string or false", node.Value)
	default:
		return fmt.Errorf("config: FlexString: unsupported YAML type %q, expected string or false", node.Tag)
	}
}

// newConfig returns a Config seeded with code-first defaults (DSG-007).
// Values present in settings.yml override these; unset keys keep them.
// Per-engine defaults (EngineConfig.Weight) are applied post-unmarshal in
// applyEngineDefaults because a default cannot be pre-seeded into a slice.
func newConfig() *Config {
	return &Config{
		Search: Search{
			BanTimeOnFail:    5,
			MaxBanTimeOnFail: 120,
		},
		Server: Server{
			Port:        8888,
			BindAddress: "127.0.0.1",
		},
		Outgoing: Outgoing{
			RequestTimeout:  3.0,
			PoolConnections: 100,
			PoolMaxsize:     20,
			EnableHTTP2:     true,
		},
	}
}

// StringList is a []string that accepts either a YAML scalar string
// (e.g. `categories: general` without brackets) or a YAML sequence. Plain
// yaml.v3 rejects a scalar when decoding into []string, while real SearXNG
// settings.yml uses both forms. Implemented with an UnmarshalYAML hook — no
// reflection (REQ-NF-004).
type StringList []string

// UnmarshalYAML implements yaml.Unmarshaler for StringList.
func (l *StringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Tag {
	case "!!str":
		*l = []string{node.Value}
		return nil
	case "!!seq":
		var values []string
		if err := node.Decode(&values); err != nil {
			return fmt.Errorf("config: StringList: %w", err)
		}
		*l = values
		return nil
	default:
		return fmt.Errorf("config: StringList: unsupported YAML type %q, expected string or list", node.Tag)
	}
}
