package migrations

import "embed"

// FS 内嵌迁移 SQL。
//
//go:embed *.sql
var FS embed.FS
