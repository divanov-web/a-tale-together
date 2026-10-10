package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// txKey — ключ транзакции в контексте (ADR-0008).
type txKey struct{}

// InTx выполняет fn в транзакции; ctx внутри fn несёт транзакцию, методы Store используют её автоматически.
// Вложенный вызов внутри транзакции переиспользует её.
func (s *Store) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres.InTx: begin: %w", err)
	}
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres.InTx: commit: %w", err)
	}
	return nil
}

// db возвращает транзакцию из контекста или пул.
func (s *Store) db(ctx context.Context) Querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return s.pool
}
