package config

import _ "embed"

//go:embed settings.default.yml
var embeddedSettings []byte

// EmbeddedSettings returns the engine settings YAML embedded at build time
// (REQ-016): the 14 engine modules enabled, no API keys. It is byte-identical
// to the canonical user copy at configs/settings.default.yml and is written
// to a temp file by cmd/nanuq-mcp when no -config flag is given.
func EmbeddedSettings() []byte {
	return embeddedSettings
}
