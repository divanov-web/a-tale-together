// Package sim — имитатор партии с ИИ-игроками (пункт 40, ADR-0034): сценарий прогона, оркестратор фаз игрока
// и раундов, ИИ-игрок на LLM, транскрипт и сводка прогона. Бот получает входящие через подмену транспорта и
// неотличим от боевого; состояние игры оркестратор читает из БД, игроку отдаёт только его ящики сообщений.
package sim

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Scenario — сценарий прогона из `sim/runs/<имя>.json`.
type Scenario struct {
	Name         string              `json:"name"`
	Rounds       int                 `json:"rounds"`          // граница рассказа `max_rounds`
	RoundMinutes int                 `json:"round_minutes"`   // страховочный дедлайн раунда (dev: от 2)
	Profile      string              `json:"profile"`         // профиль LLM прогона, глобальный по умолчанию в БД прогона
	PlayerModel  string              `json:"player_model"`    // модель ИИ-игрока (псевдоним провайдера профиля)
	MaxActions   int                 `json:"max_actions"`     // действий игрока за ход раунда; 0 — 4
	Players      []Persona           `json:"players"`         // первый — администратор мира
	Features     map[string][]string `json:"features"`        // раунд → фичи, которые игроки обязаны попробовать; `фича@username` — только этот игрок
	Budget       Budget              `json:"budget"`          // лимиты прогона
	Setup        map[string]string   `json:"setup,omitempty"` // параметры мира после диалога: `/set <ключ> <значение>`
	AutoFaction  string              `json:"auto_faction"`    // фракция персонажей без вопроса; пусто — выбор игрока
}

// Persona — ИИ-игрок: кто он в Telegram и как отыгрывает.
type Persona struct {
	Name       string `json:"name"`        // имя пользователя Telegram (first_name)
	Username   string `json:"username"`    // username без @
	JoinsRound int    `json:"joins_round"` // раунд входа; администратор входит до старта (0 или 1)
	Persona    string `json:"persona"`     // характер и предпочтения: стиль заявок, что ищет в игре
	Character  string `json:"character"`   // замысел персонажа: имя, архетип, описания; модель держится его
}

// Budget — лимиты прогона; 0 — без лимита.
type Budget struct {
	MaxCalls int     `json:"max_calls"` // вызовов LLM по игре, включая игроков
	MaxUSD   float64 `json:"max_usd"`   // USD-эквивалент по таблице тарифов
}

// Feature — фича расписания: ключ в сценарии и задание игроку.
type Feature struct {
	Key  string
	Task string
}

// Features — каталог фич расписания. Новая команда или механика игрока добавляется сюда и в сценарии
// (решение руководителя В3, пункт 40).
var Features = []Feature{
	{"scout", "Перед заявкой выполни команду /scout и учти, что увидел вокруг."},
	{"say", "Кроме заявки скажи что-нибудь от лица персонажа командой /say <реплика> — обращение к спутникам или окружающим."},
	{"quests", "Выполни /quests, прочитай цель фракции и квесты и направь заявку на ближайший этап."},
	{"party", "Выполни /party и посмотри, кто с тобой в группе; в заявке учти спутников."},
	{"character", "Выполни /character и сверь состояние персонажа (HP, инвентарь, локация) перед заявкой."},
	{"knowledge", "Выполни /knowledge и используй известные факты в заявке."},
	{"journal", "Выполни /journal и перечитай последние хроники перед заявкой."},
	{"status", "Выполни /status и сверь номер раунда и дедлайн."},
	{"bio", "Измени открытое описание персонажа: /bio <до 100 символов>, в духе прожитых событий."},
	{"secret", "Измени скрытое описание персонажа: /secret <до 100 символов> — тайна, которую знает только рассказчик."},
	{"appearance", "Обнови внешность персонажа: /appearance <до 300 символов>."},
	{"away", "В этом раунде персонаж уходит из истории: выполни /away и подтверди кнопкой «Уйти»; заявку не подавай."},
	{"back", "Персонаж возвращается: выполни /back, затем подай заявку как обычно."},
	{"group_act", "Подай заявку открыто в группе: команда /act <текст> в чате группы, не в личке."},
	{"hint", "Попроси подсказку рассказчика командой /hint (если бот ответит, что команды нет, — просто подай заявку)."},
}

// featureTask — задание по ключу фичи.
func featureTask(key string) (string, bool) {
	for _, f := range Features {
		if f.Key == key {
			return f.Task, true
		}
	}
	return "", false
}

// Load читает и проверяет сценарий из файла.
func Load(path string) (Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("sim.Load: %w", err)
	}
	var sc Scenario
	if err := json.Unmarshal(raw, &sc); err != nil {
		return Scenario{}, fmt.Errorf("sim.Load: %s: %w", path, err)
	}
	if err := sc.Validate(); err != nil {
		return Scenario{}, fmt.Errorf("sim.Load: %s: %w", path, err)
	}
	return sc, nil
}

// Validate проверяет сценарий: игроки, раунды входа, фичи из каталога, профиль.
func (sc Scenario) Validate() error {
	var errs []error
	if strings.TrimSpace(sc.Name) == "" {
		errs = append(errs, errors.New("name пуст"))
	}
	if sc.Rounds < 1 {
		errs = append(errs, errors.New("rounds должен быть не меньше 1"))
	}
	if sc.RoundMinutes < 1 {
		errs = append(errs, errors.New("round_minutes должен быть не меньше 1"))
	}
	if strings.TrimSpace(sc.Profile) == "" {
		errs = append(errs, errors.New("profile пуст"))
	}
	if strings.TrimSpace(sc.PlayerModel) == "" {
		errs = append(errs, errors.New("player_model пуст"))
	}
	if len(sc.Players) == 0 {
		errs = append(errs, errors.New("players пуст"))
	}
	names := map[string]bool{}
	for i, p := range sc.Players {
		switch {
		case strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Username) == "":
			errs = append(errs, fmt.Errorf("players[%d]: name и username обязательны", i))
		case names[p.Username]:
			errs = append(errs, fmt.Errorf("players[%d]: username %q повторяется", i, p.Username))
		}
		names[p.Username] = true
		if i == 0 && p.JoinsRound > 1 {
			errs = append(errs, errors.New("players[0] — администратор, входит до старта (joins_round 0 или 1)"))
		}
		if p.JoinsRound > sc.Rounds {
			errs = append(errs, fmt.Errorf("players[%d]: joins_round %d больше rounds %d", i, p.JoinsRound, sc.Rounds))
		}
		if strings.TrimSpace(p.Persona) == "" || strings.TrimSpace(p.Character) == "" {
			errs = append(errs, fmt.Errorf("players[%d]: persona и character обязательны", i))
		}
	}
	for round, keys := range sc.Features {
		n, err := strconv.Atoi(round)
		if err != nil || n < 1 {
			errs = append(errs, fmt.Errorf("features: раунд %q — не номер", round))
		}
		for _, k := range keys {
			key, who, _ := strings.Cut(k, "@")
			if _, ok := featureTask(key); !ok {
				errs = append(errs, fmt.Errorf("features[%s]: неизвестная фича %q (каталог sim.Features)", round, key))
			}
			if who != "" && !names[who] {
				errs = append(errs, fmt.Errorf("features[%s]: игрок %q не найден в players", round, who))
			}
		}
	}
	return errors.Join(errs...)
}

// Tasks — задания фич раунда для игрока в порядке сценария; nil — свободный раунд. Фича `ключ@username`
// достаётся только этому игроку, без адресата — всем.
func (sc Scenario) Tasks(round int, username string) []string {
	var out []string
	for _, k := range sc.Features[strconv.Itoa(round)] {
		key, who, _ := strings.Cut(k, "@")
		if who != "" && who != username {
			continue
		}
		if t, ok := featureTask(key); ok {
			out = append(out, t)
		}
	}
	return out
}

// SetupKeys — ключи параметров мира после диалога по алфавиту (порядок применения стабилен).
func (sc Scenario) SetupKeys() []string {
	keys := make([]string, 0, len(sc.Setup))
	for k := range sc.Setup {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
