// Package fake — провайдер `fake` для тестов: записанные ответы без сети.
package fake

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"taletogether/internal/model"
)

// Client — фейковый провайдер: отвечает заданным JSON или текстом, запоминает запросы.
type Client struct {
	mu       sync.Mutex
	JSON     json.RawMessage // ответ по схеме; пусто — `{"ok":true}`
	Text     string          // текстовый ответ
	Err      error           // если задан, возвращается вместо ответа
	Requests []model.LLMRequest
}

// New создаёт фейк с ответом `{"ok":true}`.
func New() *Client { return &Client{} }

// Complete возвращает записанный ответ и запоминает запрос.
func (c *Client) Complete(_ context.Context, req model.LLMRequest) (model.LLMResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Requests = append(c.Requests, req)
	if c.Err != nil {
		return model.LLMResponse{}, c.Err
	}
	out := c.JSON
	if len(out) == 0 {
		out = json.RawMessage(`{"ok":true}`)
	}
	text := c.Text
	if text == "" {
		text = string(out)
	}
	return model.LLMResponse{Text: text, JSON: out, Model: "fake-" + req.Model, TokensIn: 10, TokensOut: 5, Duration: time.Millisecond, RequestID: "fake"}, nil
}
