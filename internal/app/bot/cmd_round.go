package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"taletogether/internal/app/game"
	"taletogether/internal/app/round"
	"taletogether/internal/model"
)

// Ответы подтверждения для /begin и /finish (ADR-0015): без аргумента — вопрос с кнопками, `yes` — выполнить, `no` — отменить.
const (
	confirmYes = "yes"
	confirmNo  = "no"
)

// confirmKeyboard — кнопки подтверждения команды группы: действие и отмена.
func confirmKeyboard(command, action string) model.Keyboard {
	return model.Keyboard{{
		{Text: action, Data: model.CallbackCommand(command, confirmYes)},
		{Text: "Отмена", Data: model.CallbackCommand(command, confirmNo)},
	}}
}

// gameBegin — /begin в группе: без аргумента спрашивает подтверждение кнопками, `/begin yes` принимает запуск
// кампании: статус `starting`, стартовую сцену и первый раунд делает тик планировщика через outbox (ADR-0024), ответ
// не ждёт модели; права проверяет сценарий при выполнении, то есть у нажавшего.
func (s *Service) gameBegin(ctx context.Context, req *request) error {
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(req.in.Args)) {
	case confirmYes:
	case confirmNo:
		return s.reply(ctx, req.in, "Запуск отменён.")
	default:
		return s.replyKeyboard(ctx, req.in, fmt.Sprintf("Начать кампанию «%s»? Бот опубликует стартовую сцену и откроет первый раунд. Подтвердить: кнопкой или /begin yes", g.Title), confirmKeyboard("begin", "Начать"))
	}
	g, err = s.rounds.Begin(ctx, g, req.user.ID)
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Запускаю кампанию «%s»: стартовая сцена придёт в группу через минуту-две, затем откроется первый раунд.", g.Title))
}

// gamePause — /pause в группе: пауза или возобновление; ответ о паузе несёт кнопку «Возобновить» (ADR-0015).
func (s *Service) gamePause(ctx context.Context, req *request) error {
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	g, paused, err := s.rounds.Pause(ctx, g, req.user.ID)
	if err != nil {
		return err
	}
	if paused {
		return s.replyKeyboard(ctx, req.in, fmt.Sprintf("Игра «%s» на паузе. Возобновить: /pause", g.Title),
			model.Keyboard{{{Text: "Возобновить", Data: model.CallbackCommand("pause", "")}}})
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Игра «%s» возобновлена.", g.Title))
}

// gameFinish — /finish в группе: без аргумента спрашивает подтверждение кнопками, `/finish yes` принимает завершение:
// статус `finishing`, открытый раунд разрешает и финальную хронику ставит тик планировщика (ADR-0024).
func (s *Service) gameFinish(ctx context.Context, req *request) error {
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(req.in.Args)) {
	case confirmYes:
	case confirmNo:
		return s.reply(ctx, req.in, "Завершение отменено.")
	default:
		return s.replyKeyboard(ctx, req.in, fmt.Sprintf("Завершить игру «%s»? Открытый раунд будет разыгран, в группу придёт финальная хроника, вернуть игру нельзя. Подтвердить: кнопкой или /finish yes", g.Title), confirmKeyboard("finish", "Завершить"))
	}
	if g, err = s.rounds.Finish(ctx, g, req.user.ID); err != nil {
		return err
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Завершаю игру «%s»: открытый раунд разыгрывается, финальная хроника придёт в группу через минуту-две.", g.Title))
}

// gameReset — /reset в группе (только dev): игра в черновик, ход игры удалён, персонажи на старте.
func (s *Service) gameReset(ctx context.Context, req *request) error {
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	if g, err = s.rounds.Reset(ctx, g, req.user.ID); err != nil {
		return err
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Игра «%s» сброшена в черновик: раунды, события и сообщения удалены, персонажи на стартовой локации. Запуск: /begin.", g.Title))
}

// roundDetail — /round [номер] в группе (только dev): карточка раунда для разбора: статус, попытки, заявки с итогами, метрики.
func (s *Service) roundDetail(ctx context.Context, req *request) error {
	arg, _ := splitArgs(req.in.Args)
	no := 0
	if arg != "" {
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 {
			return s.reply(ctx, req.in, "Формат: /round [номер раунда]")
		}
		no = n
	}
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	d, err := s.rounds.Detail(ctx, g, no)
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, roundCard(g, d, s.clock.Now()))
}

// roundCard — текст карточки раунда.
func roundCard(g model.Game, d round.Detail, now time.Time) string {
	r := d.Round
	var b strings.Builder
	fmt.Fprintf(&b, "Раунд %d — %s", r.RoundNo, r.Status)
	if r.Attempts > 0 {
		fmt.Fprintf(&b, ", неудачных попыток %d", r.Attempts)
	}
	fmt.Fprintf(&b, "\nОткрыт %s, дедлайн %s", round.FormatTime(g, r.OpensAt, now), round.FormatTime(g, r.DeadlineAt, now))
	if r.ClosedAt != nil {
		fmt.Fprintf(&b, ", закрыт %s", round.FormatTime(g, *r.ClosedAt, now))
	}
	if r.SkipReason != "" {
		b.WriteString("\nПричина: " + r.SkipReason)
	}
	fmt.Fprintf(&b, "\nЗаявок: %d; итогов: %d; событий: %d", len(d.Submissions), len(d.Results), d.Events)
	if m := r.Metrics; m != nil {
		fmt.Fprintf(&b, "\nМетрики (попытка %d): всего %d мс — заморозка %d, интерпретатор %d, движок %d, рассказчик %d, публикация %d", m.Attempt, m.TotalMS, m.FreezeMS, m.InterpretMS, m.EngineMS, m.NarrateMS, m.PublishMS)
		fmt.Fprintf(&b, "\nLLM: вызовов %d, токены %d/%d; запасной разбор: %s; шаблонная хроника: %s; безопасных толкований %d, дописано событий %d, замечаний %d",
			m.LLMCalls, m.TokensIn, m.TokensOut, yesNo(m.InterpreterFallback), yesNo(m.NarratorFallback), m.Safe, m.Patched, m.Issues)
		fmt.Fprintf(&b, "\nИтоги: resolved %d, rejected %d, ambiguous %d; хроника частей %d, уведомлений %d", m.Resolved, m.Rejected, m.Ambiguous, m.ChronicleParts, m.Notices)
	}
	results := map[model.SubmissionID]model.ActionResult{}
	for _, res := range d.Results {
		results[res.SubmissionID] = res
	}
	for _, sub := range d.Submissions {
		fmt.Fprintf(&b, "\n• %s (%s): «%s»", d.Names[sub.CharacterID], sub.Status, truncate(sub.RawText, 60))
		if res, ok := results[sub.ID]; ok {
			fmt.Fprintf(&b, " → %s %s", res.Status, res.Intent.ActionType)
			if res.Summary != "" {
				b.WriteString(": " + res.Summary)
			} else if res.Reason != "" {
				b.WriteString(": " + res.Reason)
			}
		}
	}
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "да"
	}
	return "нет"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// roundNext — /next в группе (только dev): закрыть текущий раунд и открыть следующий; ответ учитывает, что после
// закрытия игра могла завершиться по границе рассказа или встать на паузу после пропущенных раундов (B11).
func (s *Service) roundNext(ctx context.Context, req *request) error {
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	g, err = s.rounds.Next(ctx, g, req.user.ID)
	if err != nil {
		return err
	}
	switch g.Status {
	case model.GamePaused:
		return s.reply(ctx, req.in, "Раунд закрыт. Игра на паузе после пропущенных подряд раундов, возобновить: /pause")
	case model.GameFinished:
		return s.reply(ctx, req.in, "Раунд закрыт, игра завершена.")
	}
	return s.reply(ctx, req.in, "Раунд закрыт, следующий открыт.")
}

// roundReplay — /replay <номер> в группе (только dev): воспроизведение раунда по снимку и seed (S06).
func (s *Service) roundReplay(ctx context.Context, req *request) error {
	arg, _ := splitArgs(req.in.Args)
	no, err := strconv.Atoi(arg)
	if err != nil || no < 1 {
		return s.reply(ctx, req.in, "Формат: /replay <номер раунда>")
	}
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	n, same, err := s.rounds.Replay(ctx, g, req.user.ID, no)
	if err != nil {
		return err
	}
	if same {
		return s.reply(ctx, req.in, fmt.Sprintf("Раунд %d воспроизведён: %d событий совпадают с журналом.", no, n))
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Раунд %d: воспроизведение расходится с журналом (%d событий записано).", no, n))
}

// act — /act <текст>: заявка персонажа на открытый раунд. В личке — текущая игра пользователя, заявка скрыта;
// в группе — игра чата, текст заявки виден всем, это выбор игрока (B14). Аргумент `#…` — ссылка на вариант сцены
// с кнопки под личным сообщением (ADR-0015): заявкой становится название подхода.
func (s *Service) act(ctx context.Context, req *request) error {
	text := strings.TrimSpace(req.in.Args)
	if text == "" {
		return s.reply(ctx, req.in, fmt.Sprintf("Формат: /act <что делает персонаж> (до %d символов). Заявка одна на раунд, изменить её нельзя.", model.SubmissionMaxLen))
	}
	g, c, err := s.actor(ctx, req)
	if err != nil {
		return err
	}
	var r model.Round
	var sub model.Submission
	if ref, ok := round.ParseOptionRef(text); ok {
		r, sub, err = s.rounds.SubmitOption(ctx, g, c, ref)
	} else {
		r, sub, err = s.rounds.Submit(ctx, g, c, text)
	}
	if err != nil {
		return err
	}
	if req.in.Callback != nil {
		return s.reply(ctx, req.in, fmt.Sprintf("Заявка принята: «%s», раунд %d, до %s.", sub.RawText, r.RoundNo, round.FormatTime(g, r.DeadlineAt, s.clock.Now())))
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Заявка принята: раунд %d, до %s.", r.RoundNo, round.FormatTime(g, r.DeadlineAt, s.clock.Now())))
}

// actor — игра и персонаж отправителя по контексту сообщения: игра чата в группе, текущая игра пользователя в личке.
func (s *Service) actor(ctx context.Context, req *request) (model.Game, model.Character, error) {
	var g model.Game
	var err error
	if req.in.IsPrivate() {
		g, err = s.games.Current(ctx, req.user)
	} else {
		g, err = s.games.OfChat(ctx, req.chat.ID)
	}
	if err != nil {
		return model.Game{}, model.Character{}, err
	}
	c, err := s.games.Character(ctx, g, req.user.ID)
	if err != nil {
		return model.Game{}, model.Character{}, err
	}
	return g, c, nil
}

// implicitAct — текст без команды в личке вне диалога (B17): если у игрока есть текущая игра с живым активным
// персонажем и открытым раундом, текст принимается как заявка; иначе подсказка про команды. Подтверждения нет
// (решение руководителя), поэтому ответ явно говорит, что текст стал заявкой.
func (s *Service) implicitAct(ctx context.Context, req *request) error {
	const hint = "Я понимаю только команды. Список: /help"
	g, c, err := s.actor(ctx, req)
	if errors.Is(err, game.ErrNoGame) || errors.Is(err, game.ErrChooseGame) || errors.Is(err, game.ErrNoCharacter) {
		return s.reply(ctx, req.in, hint)
	}
	if err != nil {
		return s.fail(ctx, req, err)
	}
	if g.Status != model.GameActive || c.Status.Absent() {
		return s.reply(ctx, req.in, hint)
	}
	r, _, err := s.rounds.Submit(ctx, g, c, req.in.Text)
	if errors.Is(err, round.ErrNoOpenRound) {
		return s.reply(ctx, req.in, hint)
	}
	if err != nil {
		return s.fail(ctx, req, err)
	}
	s.journal(ctx, model.ChatLogRecord{Kind: model.ChatLogIn, Chat: req.in.ChatID, UpdateID: req.in.UpdateID, GameID: g.ID, RoundID: r.ID, RoundNo: r.RoundNo, Event: "implicit_act"})
	return s.reply(ctx, req.in, fmt.Sprintf("Принято как заявка на раунд %d, до %s. Изменить её нельзя.", r.RoundNo, round.FormatTime(g, r.DeadlineAt, s.clock.Now())))
}

// roundLines — строки /status о раундах: текущий, предыдущий, следующий или причина простоя.
func (s *Service) roundLines(ctx context.Context, g model.Game) (current, last, next string, err error) {
	now := s.clock.Now()
	switch g.Status {
	case model.GameSetup, model.GameDraft, model.GameStarting:
		return "", "", "", nil
	case model.GamePaused:
		next = "игра на паузе; возобновить: /pause в группе"
	case model.GameFinished:
		next = "игра завершена"
	}
	info, err := s.rounds.Info(ctx, g)
	if err != nil {
		return "", "", "", err
	}
	if info.Open != nil {
		current = fmt.Sprintf("%d, заявки до %s; заявок %d из %d", info.Open.RoundNo, round.FormatTime(g, info.Open.DeadlineAt, now), info.Submissions, g.Settings.MinRoundAnswers)
		if g.Status == model.GamePaused {
			current += " (пауза)"
		}
	}
	if info.Last != nil {
		switch info.Last.Status {
		case model.RoundSkipped:
			last = fmt.Sprintf("%d пропущен: %s", info.Last.RoundNo, info.Last.SkipReason)
		case model.RoundFailed:
			last = fmt.Sprintf("%d не разрешён: %s", info.Last.RoundNo, info.Last.SkipReason)
		case model.RoundFrozen, model.RoundResolving, model.RoundResolved, model.RoundPublished:
			n := 0
			if info.Last.Snapshot != nil {
				n = len(info.Last.Snapshot.Submissions)
			}
			word := "разрешён"
			if info.Last.Status == model.RoundFrozen || info.Last.Status == model.RoundResolving {
				word = "разрешается"
			}
			last = fmt.Sprintf("%d %s, заявок %d", info.Last.RoundNo, word, n)
		default:
			last = fmt.Sprintf("%d — %s", info.Last.RoundNo, info.Last.Status)
		}
	}
	if g.Status == model.GameActive {
		switch {
		case info.Open != nil:
			next = "сразу после текущего, в " + round.FormatTime(g, info.Open.DeadlineAt, now)
		case g.NextRoundAt != nil:
			next = fmt.Sprintf("в %s (игровое окно %s–%s)", round.FormatTime(g, *g.NextRoundAt, now), g.PlayFrom, g.PlayTo)
		default:
			next = "по расписанию"
		}
	}
	return current, last, next, nil
}
