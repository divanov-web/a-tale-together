package model

import "errors"

// ErrNotFound — запись не найдена в хранилище; проверяется через errors.Is.
var ErrNotFound = errors.New("not found")
