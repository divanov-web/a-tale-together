package sim

import (
	"context"

	"taletogether/internal/model"
)

// Client — провайдер LLM (как `app/llm.Client`).
type Client interface {
	Complete(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error)
}

// ModelOverride — обёртка провайдера: подменяет модель по роли вызова, не меняя профили в БД
// (флаги `-model-narrator`, `-model-interpreter`, `-model-player`; решение руководителя В1).
type ModelOverride struct {
	Client Client
	Models map[model.LLMRole]string // роль → модель; пусто — модель профиля
}

// Complete подставляет модель роли и передаёт запрос провайдеру.
func (o ModelOverride) Complete(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
	if m := o.Models[req.Role]; m != "" {
		req.Model = m
	}
	return o.Client.Complete(ctx, req)
}

// RoleModels разбирает флаги подмены моделей; пустые значения пропускаются.
func RoleModels(narrator, interpreter, player string) map[model.LLMRole]string {
	out := map[model.LLMRole]string{}
	if narrator != "" {
		out[model.RoleNarrator], out[model.RoleOpening], out[model.RoleWorldgen] = narrator, narrator, narrator
	}
	if interpreter != "" {
		out[model.RoleInterpreter] = interpreter
	}
	if player != "" {
		out[model.RoleSimPlayer] = player
	}
	return out
}
