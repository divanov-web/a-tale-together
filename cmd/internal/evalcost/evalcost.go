// Package evalcost — тарифы моделей для консольных оценочных наборов (пункт 33, ADR-0032): команды `cmd/eval-*`
// вызывают провайдер напрямую, мимо `app/llm`, поэтому цену вызова считают сами по таблице `llm_prices`.
package evalcost

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"taletogether/internal/adapter/postgres"
	"taletogether/internal/model"
)

// Load читает тарифы из БД; без базы печатает предупреждение и возвращает пустую таблицу (цена 0).
func Load(ctx context.Context, dsn string, logger *slog.Logger) model.LLMPriceTable {
	store, err := postgres.New(ctx, postgres.Settings{DSN: dsn}, logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, "тарифы недоступны, цена 0:", err)
		return model.LLMPriceTable{}
	}
	defer store.Close()
	prices, err := store.ListLLMPrices(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "тарифы недоступны, цена 0:", err)
		return model.LLMPriceTable{}
	}
	return model.NewLLMPriceTable(prices)
}

// Price проставляет ответу цену по таблице; неизвестная модель — 0 и предупреждение в stderr.
func Price(table model.LLMPriceTable, resp *model.LLMResponse) {
	if resp.Model == "" {
		return
	}
	cost, ok := table.Estimate(resp.Model, resp.Usage)
	if !ok {
		fmt.Fprintf(os.Stderr, "тариф модели %s неизвестен, цена 0\n", resp.Model)
	}
	resp.CostUSD = cost
}

// Print — строка одного вызова для `-v`: модель, токены по видам, цена по таблице и оценка провайдера.
func Print(resp model.LLMResponse) {
	u := resp.Usage
	fmt.Printf("     вызов %s: вход %d (без кэша %d, запись кэша %d, чтение %d), выход %d, цена $%.4f, оценка провайдера $%.4f, %s\n",
		resp.Model, u.TotalIn(), u.Input, u.CacheWrite, u.CacheRead, u.Output, resp.CostUSD, resp.ProviderCostUSD, resp.Duration.Round(1e8))
}
