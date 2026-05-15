// Package web holds the dashboard's HTML templates and static assets.
// They are embedded into the binary, so "backendlab serve" needs no files
// besides its configuration and the approved suites directory.
package web

import "embed"

// Templates holds templates/*.html: layout.html and partials.html are
// shared by every page, the other files each define one page's content.
//
//go:embed templates/*.html
var Templates embed.FS

// Static holds static/app.css and static/app.js, served under /static/.
//
//go:embed static
var Static embed.FS
