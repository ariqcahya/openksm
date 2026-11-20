package migrations

import "embed"

//go:embed registry/*.sql tenant/*.sql
var FS embed.FS
