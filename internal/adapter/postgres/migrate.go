package postgres

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"taletogether/migrations"
)

// Migrate применяет встроенные миграции goose, ещё не применённые к базе.
func (s *Store) Migrate(ctx context.Context) error {
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close() // пул остаётся открытым: закрывается только обёртка database/sql

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("postgres.Migrate: provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("postgres.Migrate: up: %w", err)
	}
	for _, r := range results {
		s.logger.Info("migration applied", slog.String("file", r.Source.Path), slog.Duration("took", r.Duration))
	}
	return nil
}
