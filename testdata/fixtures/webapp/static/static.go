// Package static embeds browser assets.
package static

import "embed"

// FS holds the files served under /static/.
//
//go:embed app.css htmx.min.js logo.svg
var FS embed.FS
