// Package templates embeds the HTML templates.
package templates

import "embed"

// FS holds every page template.
//
//go:embed *.html
var FS embed.FS
