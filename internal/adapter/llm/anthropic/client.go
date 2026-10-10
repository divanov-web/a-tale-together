// Package anthropic — заготовка провайдера `anthropic-api` (Anthropic); реализация в пункте плана 08.
package anthropic

import (
	"context"
	"errors"

	"taletogether/internal/model"
)

// ErrNotImplemented — провайдер зарегистрирован, но ещё не реализован.
var ErrNotImplemented = errors.New("провайдер anthropic-api ещё не реализован (пункт плана 08)")

// Client — заглушка провайдера.
type Client struct{}

// New создаёт заглушку.
func New() *Client { return &Client{} }

// Complete всегда возвращает ErrNotImplemented.
func (*Client) Complete(context.Context, model.LLMRequest) (model.LLMResponse, error) {
	return model.LLMResponse{}, ErrNotImplemented
}
