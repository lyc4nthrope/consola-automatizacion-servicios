// Package web embeds the console's static assets and HTML templates so the binary works offline.
package web

import "embed"

// Static holds web/static, served under /static/.
//
//go:embed static
var Static embed.FS

// Templates holds web/templates: the shared layout, one file per page and the partials.
// "all:" is required: without it go:embed silently skips files starting with "_",
// which is exactly how partials are named.
//
//go:embed all:templates
var Templates embed.FS
