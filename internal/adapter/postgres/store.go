// Package postgres — хранилище: пул pgx, миграции goose, репозитории по сущностям.
package postgres

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Settings — настройки подключения.
type Settings struct {
	DSN string // DSN PostgreSQL
}

// Querier — общий интерфейс пула и транзакции; репозитории работают только через него.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store — вход в хранилище; методы репозиториев объявлены в файлах по сущностям.
type Store struct {
	pool   *pgxpool.Pool
	q      Querier
	logger *slog.Logger
}

// New открывает пул и проверяет соединение.
func New(ctx context.Context, s Settings, logger *slog.Logger) (*Store, error) {
	pool, err := pgxpool.New(ctx, s.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres.New: pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres.New: ping: %w", err)
	}
	return &Store{pool: pool, q: pool, logger: logger}, nil
}

// Close закрывает пул; ждёт возврата занятых соединений.
func (s *Store) Close() { s.pool.Close() }
