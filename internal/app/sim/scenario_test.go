package sim

import (
	"strings"
	"testing"
)

func validScenario() Scenario {
	return Scenario{Name: "t", Rounds: 3, RoundMinutes: 2, Profile: "sim-haiku", PlayerModel: "haiku",
		Players: []Persona{
			{Name: "A", Username: "a", Persona: "p", Character: "c"},
			{Name: "B", Username: "b", JoinsRound: 2, Persona: "p", Character: "c"},
		},
		Features: map[string][]string{"2": {"scout", "say@b"}}}
}

// TestLoad_Basic: сценарий репозитория проходит проверку, фичи расписания известны каталогу.
func TestLoad_Basic(t *testing.T) {
	sc, err := Load("../../../sim/runs/basic.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Players) != 3 || sc.Rounds != 10 || sc.Players[1].JoinsRound != 2 || sc.Players[2].JoinsRound != 3 {
		t.Fatalf("сценарий: %+v", sc)
	}
	if tasks := sc.Tasks(4, "marta_sim"); len(tasks) != 1 || !strings.Contains(tasks[0], "/scout") {
		t.Fatalf("задания раунда 4: %v", tasks)
	}
}

// TestValidate: неизвестная фича, повтор username, чужой адресат фичи и поздний вход администратора — ошибки.
func TestValidate(t *testing.T) {
	if err := validScenario().Validate(); err != nil {
		t.Fatalf("валидный сценарий: %v", err)
	}
	bad := validScenario()
	bad.Features["3"] = []string{"teleport"}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "teleport") {
		t.Fatalf("неизвестная фича: %v", err)
	}
	bad = validScenario()
	bad.Features["3"] = []string{"scout@zz"}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "zz") {
		t.Fatalf("адресат фичи: %v", err)
	}
	bad = validScenario()
	bad.Players[1].Username = "a"
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "повторяется") {
		t.Fatalf("повтор username: %v", err)
	}
	bad = validScenario()
	bad.Players[0].JoinsRound = 2
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "администратор") {
		t.Fatalf("администратор позже старта: %v", err)
	}
	bad = validScenario()
	bad.Players[1].JoinsRound = 9
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "больше rounds") {
		t.Fatalf("вход за границей: %v", err)
	}
}

// TestTasks_Addressed: фича с адресатом достаётся только ему, общая — всем; свободный раунд без заданий.
func TestTasks_Addressed(t *testing.T) {
	sc := validScenario()
	if got := sc.Tasks(2, "a"); len(got) != 1 || !strings.Contains(got[0], "/scout") {
		t.Fatalf("игрок a: %v", got)
	}
	if got := sc.Tasks(2, "b"); len(got) != 2 || !strings.Contains(got[1], "/say") {
		t.Fatalf("игрок b: %v", got)
	}
	if got := sc.Tasks(1, "a"); got != nil {
		t.Fatalf("свободный раунд: %v", got)
	}
}
