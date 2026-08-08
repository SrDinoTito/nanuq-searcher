// Package templates embeds the HTML templates served by the webapp
// (TASK-012a). All templates are rendered with html/template so that
// autoescaping is always on (REQ-NF-005); text/template is never used.
package templates

import "embed"

// FS embeds every *.html file in this directory into the binary at build
// time. Templates are parsed once at startup by the handlers package.
//
//go:embed *.html
var FS embed.FS
