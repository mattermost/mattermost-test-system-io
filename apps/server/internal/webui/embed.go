// Package webui serves the embedded React single-page application bundle
// that is compiled into the server binary at build time. The production
// server self-hosts the UI on the same port as the API; the Vite dev
// server is only used during local web development.
package webui

import "embed"

//go:embed dist
var distFS embed.FS
