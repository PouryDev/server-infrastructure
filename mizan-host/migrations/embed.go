// Package migrations embeds the SQL files so the migrate binary carries its schema with it.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
