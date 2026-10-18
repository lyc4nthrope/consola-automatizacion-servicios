// Package web embeds the console's static assets so the binary works offline.
package web

import "embed"

// Static holds web/static, served under /static/.
//
//go:embed static
var Static embed.FS
