// Package templates — тексты ответов бота на русском (text/template), без логики сценариев.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed *.tmpl
var files embed.FS

var set = template.Must(template.ParseFS(files, "*.tmpl"))

// Render выполняет шаблон `name.tmpl` с данными; ошибка шаблона — дефект сборки, не ввода.
func Render(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, name+".tmpl", data); err != nil {
		return "", fmt.Errorf("templates.Render %s: %w", name, err)
	}
	return buf.String(), nil
}
