// Package openai — заготовка провайдера `openai-api` (OpenAI); реализация в пункте плана 08.
package openai

import (
	"context"
	"errors"

	"taletogether/internal/model"
)

// ErrNotImplemented — провайдер зарегистрирован, но ещё не реализован.
var ErrNotImplemented = errors.New("провайдер openai-api ещё не реализован (пункт плана 08)")

// Client — заглушка провайдера.
type Client struct{}

// New создаёт заглушку.
func New() *Client { return &Client{} }

// Complete всегда возвращает ErrNotImplemented.
func (*Client) Complete(context.Context, model.LLMRequest) (model.LLMResponse, error) {
	return model.LLMResponse{}, ErrNotImplemented
}
