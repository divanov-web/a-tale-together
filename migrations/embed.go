// Package migrations встраивает SQL-миграции goose в бинарник для применения при старте.
package migrations

import "embed"

// FS — файлы миграций `NNNNN_<что>.sql`.
//
//go:embed *.sql
var FS embed.FS
