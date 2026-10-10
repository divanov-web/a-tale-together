package round

import (
	"strconv"
	"strings"
	"time"
)

// location — часовой пояс игры; неизвестный пояс считается UTC.
func location(timezone string) *time.Location {
	loc, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" {
		return time.UTC
	}
	return loc
}

// clockMinutes разбирает «HH:MM» в минуты от начала суток.
func clockMinutes(v string) (int, bool) {
	h, m, ok := strings.Cut(v, ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// inWindow сообщает, попадает ли момент в игровое окно; без окна — всегда. Окно через полночь (22:00–02:00) поддерживается.
func inWindow(timezone, from, to string, at time.Time) bool {
	f, ok1 := clockMinutes(from)
	t, ok2 := clockMinutes(to)
	if !ok1 || !ok2 || f == t {
		return true
	}
	local := at.In(location(timezone))
	m := local.Hour()*60 + local.Minute()
	if f < t {
		return m >= f && m < t
	}
	return m >= f || m < t
}

// nextWindowStart — ближайшее начало игрового окна после момента at.
func nextWindowStart(timezone, from string, at time.Time) time.Time {
	f, ok := clockMinutes(from)
	if !ok {
		return at
	}
	loc := location(timezone)
	local := at.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), f/60, f%60, 0, 0, loc)
	if !start.After(at) {
		start = start.AddDate(0, 0, 1)
	}
	return start
}
