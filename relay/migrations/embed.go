package migrations

import "embed"

// Files contains the application SQL migrations.
//
//go:embed *.sql
var Files embed.FS
