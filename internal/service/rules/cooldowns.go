package rules

import "taletogether/internal/model"

// CooldownRounds — длительности перезарядок в раундах (rules_version 1, ADR-0033): действие, выполненное в раунде N,
// снова доступно с раунда N + длительность. Действия без записи здесь перезарядки не имеют.
var CooldownRounds = map[model.CooldownAction]int{
	model.CooldownBio:    10,
	model.CooldownSecret: 10,
	model.CooldownRename: 100,
	model.CooldownHint:   5,
}

// CooldownState — итог проверки перезарядки в текущем раунде.
type CooldownState struct {
	Ready   bool // действие доступно
	Left    int  // раундов до доступности; 0, если доступно
	ReadyAt int  // номер раунда, с которого доступно; 0, если действие ещё не выполнялось
}

// CheckCooldown проверяет, доступно ли действие в раунде round, если в последний раз оно выполнено в раунде lastRound
// (0 — не выполнялось). До первого раунда (round 0) перезарядки нет.
func CheckCooldown(action model.CooldownAction, lastRound, round int) CooldownState {
	d := CooldownRounds[action]
	if lastRound <= 0 || round <= 0 || d <= 0 {
		return CooldownState{Ready: true}
	}
	readyAt := lastRound + d
	if round >= readyAt {
		return CooldownState{Ready: true, ReadyAt: readyAt}
	}
	return CooldownState{Left: readyAt - round, ReadyAt: readyAt}
}
