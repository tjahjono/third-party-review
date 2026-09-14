// Package migrations embeds the SQL migration files so they travel with the
// binary and run on startup without a separate tool in the image.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
