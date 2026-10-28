// Package migrations embeds the SQL schema files, applied in filename order.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
