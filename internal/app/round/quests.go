package round

import (
	"context"
	"encoding/json"
	"fmt"

	"taletogether/internal/model"
	"taletogether/internal/service/engine"
	"taletogether/internal/service/narrator"
	"taletogether/internal/service/quest"
)

// Пункт 28 (docs/game/world.md «Квесты», ADR-0020): цели и квесты фракций. Код планирует задания (`service/quest.Plan`),
// рассказчик возвращает квесты, размещения этапов и цели в пакете `world`, код пишет строки и события в транзакции
// раунда и закрывает этапы по условиям (`service/quest.Check`) до вызова рассказчика и после привязки.

// questState — квесты, цели, фракции и карта игры в раунде; меняется по ходу разрешения.
type questState struct {
	factions []model.Faction
	quests   []model.Quest
	goals    []model.FactionGoal
	all      []model.Location
	template model.GenreTemplate
}

func (qs *questState) faction(id model.FactionID) (model.Faction, bool) {
	for _, f := range qs.factions {
		if f.ID == id {
			return f, true
		}
	}
	return model.Faction{}, false
}

func (qs *questState) goal(f model.FactionID) (model.FactionGoal, bool) {
	for _, g := range qs.goals {
		if g.FactionID == f {
			return g, true
		}
	}
	return model.FactionGoal{}, false
}

func (qs *questState) goalText(f model.FactionID) string {
	if g, ok := qs.goal(f); ok {
		return g.Text
	}
	return ""
}

func (qs *questState) quest(id model.QuestID) (*model.Quest, bool) {
	for i := range qs.quests {
		if qs.quests[i].ID == id {
			return &qs.quests[i], true
		}
	}
	return nil, false
}

func (qs *questState) cell(id model.LocationID) (model.Location, bool) {
	for _, l := range qs.all {
		if l.ID == id {
			return l, true
		}
	}
	return model.Location{}, false
}

// templateGoal — цель фракции по умолчанию из шаблона жанра по коду фракции.
func (qs *questState) templateGoal(f model.Faction) string {
	for _, t := range qs.template.Factions {
		if t.Code == f.Code {
			return t.Goal
		}
	}
	return ""
}

// loadQuests читает фракции (счётчики после открытий раунда), квесты с этапами, текущие цели и карту игры.
func (s *Service) loadQuests(ctx context.Context, g model.Game, genre model.Genre) (questState, error) {
	qs := questState{template: genre.Template}
	var err error
	if qs.factions, err = s.store.ListFactions(ctx, g.ID); err != nil {
		return qs, err
	}
	if qs.quests, err = s.store.ListQuests(ctx, g.ID); err != nil {
		return qs, err
	}
	if qs.goals, err = s.store.ListFactionGoals(ctx, g.ID); err != nil {
		return qs, err
	}
	if qs.all, err = s.store.ListLocations(ctx, g.ID); err != nil {
		return qs, err
	}
	return qs, nil
}

// questTasks планирует задания по квестам для рассказчика: новые квесты по счётчику, размещение этапов в клетках
// групп, пересмотр целей.
func (s *Service) questTasks(g model.Game, qs *questState, state *model.RoundSnapshot) (quests []narrator.QuestTask, stages []narrator.StageTask, goals []narrator.GoalTask) {
	story, side := g.Settings.QuestLimits()
	plan := quest.Plan(quest.Input{Factions: qs.factions, Quests: qs.quests, Goals: qs.goals, State: state, Locations: qs.all, StoryLimit: story, SideLimit: side})
	for _, t := range plan.Issue {
		f, _ := qs.faction(t.FactionID)
		minStages, maxStages := model.StageBounds(t.Kind)
		quests = append(quests, narrator.QuestTask{FactionID: f.ID, Faction: string(f.Code), Kind: t.Kind, Goal: qs.goalText(f.ID), MinStages: minStages, MaxStages: maxStages, Threshold: t.Threshold, Mandatory: t.Mandatory})
	}
	for _, t := range plan.Bind {
		task := narrator.StageTask{QuestID: t.Quest.ID, QuestTitle: t.Quest.Title, StageKey: t.Stage.Key, StageTitle: t.Stage.Title, StageSummary: t.Stage.Summary,
			ConditionKind: t.Stage.Condition.Kind, LocationID: t.LocationID, Objects: []string{}, Stage: t.Stage}
		if l, ok := state.Location(t.LocationID); ok {
			task.CellName, task.CellDesc = l.Name, l.Description
			for _, o := range l.Objects {
				task.Objects = append(task.Objects, o.Key)
				task.Existing = append(task.Existing, o)
			}
		}
		stages = append(stages, task)
	}
	for _, t := range plan.Goals {
		f, _ := qs.faction(t.FactionID)
		task := narrator.GoalTask{FactionID: f.ID, Faction: string(f.Code), Current: t.Current.Text, Stage: t.Current.Stage, Fallback: qs.templateGoal(f)}
		if task.Fallback == "" {
			task.Fallback = f.Description
		}
		if t.Closed != nil {
			task.Closed, task.QuestID = t.Closed.Title, &t.Closed.ID
		}
		goals = append(goals, task)
	}
	return quests, stages, goals
}

// questContext — активные квесты для подсказок рассказчика: одной строкой, ближайший этап подробно, закрытые фразой.
func questContext(qs *questState) []narrator.QuestInfo {
	var out []narrator.QuestInfo
	for _, q := range qs.quests {
		if q.Status != model.QuestActive {
			continue
		}
		f, _ := qs.faction(q.FactionID)
		info := narrator.QuestInfo{ID: q.ID, Faction: string(f.Code), Kind: q.Kind, Title: q.Title, Summary: q.Summary, Goal: qs.goalText(q.FactionID), Closed: []string{}}
		for _, st := range q.Stages {
			if st.Status == model.StageDone {
				info.Closed = append(info.Closed, st.Title)
			}
		}
		if cur, ok := q.Current(); ok {
			next := narrator.QuestStageInfo{Title: cur.Title, Summary: cur.Summary, Placed: cur.Status == model.StageBound}
			if next.Placed && cur.LocationID != nil {
				if l, ok := qs.cell(*cur.LocationID); ok {
					next.Cell = l.Name
					next.Hint = narrator.ConditionWordsFor(cur.Condition, l.Objects)
				}
			}
			info.Next = &next
		}
		out = append(out, info)
	}
	return out
}

// checkQuests закрывает этапы по условиям состояния после свёртки: `quest_stage_done` с наградой (факт уходит в
// знания персонажей фракции через `engine.Apply`), последний этап — `quest_done`, и тогда счётчик исследования
// фракции обнуляется. Строки квеста и этапа обновляются в той же транзакции.
func (s *Service) checkQuests(ctx context.Context, g model.Game, r model.Round, state *model.RoundSnapshot, qs *questState) ([]model.Event, error) {
	done := quest.Check(state, qs.quests)
	if len(done) == 0 {
		return nil, nil
	}
	var events []model.Event
	for _, d := range done {
		q, ok := qs.quest(d.Quest.ID)
		if !ok {
			continue
		}
		rid := r.ID
		for i := range q.Stages {
			if q.Stages[i].ID == d.Stage.ID {
				q.Stages[i].Status, q.Stages[i].DoneRoundID = model.StageDone, &rid
				if err := s.store.UpdateQuestStage(ctx, q.Stages[i]); err != nil {
					return nil, err
				}
			}
		}
		who := factionCharacters(state, q.FactionID)
		events = append(events, questEvent(g, r, model.EventQuestStageDone, who, map[string]any{"quest_id": q.ID, "quest": q.Title, "faction_id": q.FactionID, "kind": q.Kind,
			"stage_key": d.Stage.Key, "stage": d.Stage.Title, "stage_no": d.Stage.StageNo, "stages": len(q.Stages), "reward": d.Stage.Reward}))
		if d.QuestDone {
			q.Status, q.DoneRoundID = model.QuestDone, &rid
			if err := s.store.UpdateQuest(ctx, *q); err != nil {
				return nil, err
			}
			events = append(events, questEvent(g, r, model.EventQuestDone, who, map[string]any{"quest_id": q.ID, "quest": q.Title, "faction_id": q.FactionID, "kind": q.Kind}))
			if err := s.setExplored(ctx, g, qs, q.FactionID, 0); err != nil {
				return nil, err
			}
		}
	}
	if err := s.commitEvents(ctx, g, state, events); err != nil {
		return nil, err
	}
	// Награда свёрнута в знания адресатов; состояние персонажей уже записано раундом, поэтому знания пишутся отдельно.
	for _, ev := range events {
		if ev.Type != model.EventQuestStageDone {
			continue
		}
		for _, id := range ev.VisibleTo {
			if c, ok := state.Character(id); ok {
				if err := s.store.SaveCharacterKnowledge(ctx, g.ID, id, c.Knowledge); err != nil {
					return nil, err
				}
			}
		}
	}
	return events, nil
}

// applyQuests записывает принятые квесты, размещения этапов и цели: строки и события в транзакции раунда;
// счётчик фракции уменьшается на порог выданного квеста (лишние открытия раунда переходят в следующий интервал).
func (s *Service) applyQuests(ctx context.Context, g model.Game, r model.Round, state *model.RoundSnapshot, pkg narrator.WorldPackage, qs *questState, m *model.RoundMetrics) ([]model.Event, error) {
	var events []model.Event
	rid := r.ID
	for _, gq := range pkg.Quests {
		q := model.Quest{GameID: g.ID, FactionID: gq.FactionID, Kind: gq.Kind, Status: model.QuestActive, Title: gq.Title, Summary: gq.Summary, Stages: gq.Stages, CreatedRoundID: &rid}
		created, err := s.store.CreateQuest(ctx, q)
		if err != nil {
			return nil, err
		}
		qs.quests = append(qs.quests, created)
		titles := make([]string, 0, len(created.Stages))
		for _, st := range created.Stages {
			titles = append(titles, st.Title)
		}
		source := "model"
		if gq.Template {
			source = "template"
		}
		events = append(events, questEvent(g, r, model.EventQuestIssued, factionCharacters(state, gq.FactionID), map[string]any{"quest_id": created.ID, "faction_id": gq.FactionID, "kind": gq.Kind,
			"quest": created.Title, "summary": created.Summary, "stages": titles, "source": source}))
		if f, ok := qs.faction(gq.FactionID); ok && gq.Threshold > 0 {
			if err := s.setExplored(ctx, g, qs, f.ID, max(0, f.ExploredSinceReset-gq.Threshold)); err != nil {
				return nil, err
			}
		}
		m.QuestsIssued++
	}
	for _, gs := range pkg.Stages {
		q, ok := qs.quest(gs.QuestID)
		if !ok {
			continue
		}
		if len(gs.Objects) > 0 {
			if err := s.store.CreateObjects(ctx, g.ID, gs.LocationID, gs.Objects); err != nil {
				return nil, err
			}
			loc := gs.LocationID
			for _, o := range gs.Objects {
				b, _ := json.Marshal(map[string]any{"location_id": loc, "object": o, "quest_id": q.ID})
				events = append(events, model.Event{GameID: g.ID, RoundID: r.ID, Type: model.EventObjectCreated, Payload: b, Visibility: model.VisibilityLocation, LocationID: &loc})
			}
		}
		var placed model.QuestStage
		for i := range q.Stages {
			if q.Stages[i].Key == gs.Stage.Key {
				loc := gs.LocationID
				q.Stages[i].Status, q.Stages[i].LocationID, q.Stages[i].Condition = model.StageBound, &loc, gs.Condition
				if err := s.store.UpdateQuestStage(ctx, q.Stages[i]); err != nil {
					return nil, err
				}
				placed = q.Stages[i]
			}
		}
		cellName := ""
		if l, ok := state.Location(gs.LocationID); ok {
			cellName = l.Name
			for _, o := range gs.Objects {
				if _, _, exists := l.Object(o.Key); !exists {
					l.Objects = append(l.Objects, o)
				}
			}
		}
		for i := range qs.all {
			if qs.all[i].ID == gs.LocationID {
				qs.all[i].Objects = append(qs.all[i].Objects, gs.Objects...)
			}
		}
		source := "model"
		if gs.Template {
			source = "template"
		}
		events = append(events, questEvent(g, r, model.EventQuestStagePlaced, factionCharacters(state, q.FactionID), map[string]any{"quest_id": q.ID, "quest": q.Title, "faction_id": q.FactionID,
			"stage_key": placed.Key, "stage": placed.Title, "stage_no": placed.StageNo, "location_id": gs.LocationID, "cell": cellName, "condition": gs.Condition, "source": source}))
		m.StagesPlaced++
	}
	for _, gg := range pkg.Goals {
		created, err := s.store.CreateFactionGoal(ctx, model.FactionGoal{GameID: g.ID, FactionID: gg.FactionID, Text: gg.Text, Stage: gg.Stage, QuestID: gg.QuestID, RoundID: &rid})
		if err != nil {
			return nil, err
		}
		replaced := false
		for i := range qs.goals {
			if qs.goals[i].FactionID == gg.FactionID {
				qs.goals[i], replaced = created, true
			}
		}
		if !replaced {
			qs.goals = append(qs.goals, created)
		}
		events = append(events, questEvent(g, r, model.EventGoalRevised, factionCharacters(state, gg.FactionID), map[string]any{"faction_id": gg.FactionID, "version": created.Version, "text": created.Text, "stage": created.Stage}))
	}
	if err := s.commitEvents(ctx, g, state, events); err != nil {
		return nil, err
	}
	return events, nil
}

// setExplored пишет счётчик исследования фракции в хранилище и в памяти раунда.
func (s *Service) setExplored(ctx context.Context, g model.Game, qs *questState, f model.FactionID, n int) error {
	if err := s.store.SetFactionExplored(ctx, g.ID, f, n); err != nil {
		return err
	}
	for i := range qs.factions {
		if qs.factions[i].ID == f {
			qs.factions[i].ExploredSinceReset = n
		}
	}
	return nil
}

// startQuests создаёт цели и сюжетные квесты фракций при `/begin` (стартовый пакет, без событий: раундов ещё нет).
func (s *Service) startQuests(ctx context.Context, g model.Game, quests []narrator.GeneratedQuest, goals []narrator.GeneratedGoal) error {
	for _, gg := range goals {
		if _, err := s.store.CreateFactionGoal(ctx, model.FactionGoal{GameID: g.ID, FactionID: gg.FactionID, Text: gg.Text, Stage: gg.Stage}); err != nil {
			return err
		}
	}
	for _, gq := range quests {
		if _, err := s.store.CreateQuest(ctx, model.Quest{GameID: g.ID, FactionID: gq.FactionID, Kind: gq.Kind, Status: model.QuestActive, Title: gq.Title, Summary: gq.Summary, Stages: gq.Stages}); err != nil {
			return err
		}
	}
	return nil
}

// startQuestTasks — задания завязки: цель и сюжетный квест каждой фракции.
func startQuestTasks(factions []model.Faction, t model.GenreTemplate) (quests []narrator.QuestTask, goals []narrator.GoalTask) {
	qs := questState{template: t}
	for _, f := range factions {
		minStages, maxStages := model.StageBounds(model.QuestStory)
		quests = append(quests, narrator.QuestTask{FactionID: f.ID, Faction: string(f.Code), Kind: model.QuestStory, MinStages: minStages, MaxStages: maxStages, Mandatory: true})
		fallback := qs.templateGoal(f)
		if fallback == "" {
			fallback = f.Description
		}
		goals = append(goals, narrator.GoalTask{FactionID: f.ID, Faction: string(f.Code), Fallback: fallback})
	}
	return quests, goals
}

// factionCharacters — персонажи фракции в снимке (адресаты приватных событий квестов).
func factionCharacters(state *model.RoundSnapshot, f model.FactionID) []model.CharacterID {
	var out []model.CharacterID
	if state == nil {
		return out
	}
	for _, c := range state.Characters {
		if c.FactionID == f {
			out = append(out, c.ID)
		}
	}
	return out
}

// questEvent — приватное событие квеста персонажам фракции.
func questEvent(g model.Game, r model.Round, t model.EventType, who []model.CharacterID, payload map[string]any) model.Event {
	b, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("round.questEvent: %v", err))
	}
	return model.Event{GameID: g.ID, RoundID: r.ID, Type: t, Payload: b, Visibility: model.VisibilityPrivate, VisibleTo: who}
}

// commitEvents нумерует события за уже записанными, пишет их в журнал и сворачивает в состояние.
func (s *Service) commitEvents(ctx context.Context, g model.Game, state *model.RoundSnapshot, events []model.Event) error {
	if len(events) == 0 {
		return nil
	}
	offset, err := s.store.NextEventSeq(ctx, g.ID)
	if err != nil {
		return err
	}
	for i := range events {
		events[i].Seq = offset + int64(i)
	}
	if err := s.store.AppendEvents(ctx, events); err != nil {
		return err
	}
	return engine.Apply(state, events)
}
