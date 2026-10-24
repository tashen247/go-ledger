// Package migrations holds the embedded SQL schema used by golang-migrate.
package migrations

import "embed"

// FS contains the versioned up and down scripts.
//
//go:embed *.sql
var FS embed.FS
