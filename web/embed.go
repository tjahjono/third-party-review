// Package web embeds the templates and static assets into the binary, so the
// container image needs no volume and cannot serve a stale asset after a
// deploy.
package web

import "embed"

//go:embed templates/layouts/*.html templates/pages/*.html templates/partials/*.html
//go:embed static/css/*.css static/js/*.js
var FS embed.FS
