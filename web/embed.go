// Package web embeds the console's static assets and HTML templates so the binary works offline.
package web

import "embed"

// Static holds web/static, served under /static/.
//
//go:embed static
var Static embed.FS

// Templates holds web/templates: the shared layout and one file per page.
//
//go:embed templates
var Templates embed.FS
