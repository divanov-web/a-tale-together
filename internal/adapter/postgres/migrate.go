package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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

// MigrationState — одна миграция в статусе: номер, файл, применена ли и когда.
type MigrationState struct {
	Version   int64
	Path      string
	Applied   bool
	AppliedAt time.Time
}

// MigrationStatus возвращает все встроенные миграции с признаком применения к базе.
func (s *Store) MigrationStatus(ctx context.Context) ([]MigrationState, error) {
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return nil, fmt.Errorf("postgres.MigrationStatus: provider: %w", err)
	}
	statuses, err := provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres.MigrationStatus: status: %w", err)
	}
	out := make([]MigrationState, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, MigrationState{Version: st.Source.Version, Path: st.Source.Path, Applied: st.State == goose.StateApplied, AppliedAt: st.AppliedAt})
	}
	return out, nil
}
