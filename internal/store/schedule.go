package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Schedule says when an access may be used: a list of windows, each one with
// the days of the week and the hours of the day it covers. An empty schedule
// means always.
type Schedule struct {
	Windows []Window `json:"windows"`
}

// Window is a range of hours on some days of the week. Days follow
// time.Weekday (0 = Sunday … 6 = Saturday); Start and End are "HH:MM", and
// End may be "24:00". A window that ends before it starts goes through
// midnight (e.g. 22:00–06:00), and its days are the ones it starts on.
type Window struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
}

var weekdayNames = [7]string{"dom", "seg", "ter", "qua", "qui", "sex", "sáb"}

// minutes parses "HH:MM" (up to "24:00") into minutes since midnight.
func minutes(s string) (int, bool) {
	var h, m int
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil || m < 0 || m > 59 || h < 0 || h > 24 || (h == 24 && m != 0) {
		return 0, false
	}
	return h*60 + m, true
}

// Validate checks the windows and puts their days in order.
func (sc *Schedule) Validate() error {
	for i := range sc.Windows {
		w := &sc.Windows[i]
		start, okS := minutes(w.Start)
		end, okE := minutes(w.End)
		if !okS || !okE {
			return invalid("horário inválido: use HH:MM")
		}
		if start == end {
			return invalid("o horário final deve ser diferente do inicial")
		}
		if len(w.Days) == 0 {
			return invalid("escolha ao menos um dia da semana em cada horário")
		}
		seen := map[int]bool{}
		days := make([]int, 0, len(w.Days))
		for _, d := range w.Days {
			if d < 0 || d > 6 {
				return invalid("dia da semana inválido")
			}
			if !seen[d] {
				seen[d] = true
				days = append(days, d)
			}
		}
		sort.Ints(days)
		w.Days = days
	}
	return nil
}

func (w Window) has(day int) bool {
	for _, d := range w.Days {
		if d == day {
			return true
		}
	}
	return false
}

// Allows reports whether t (already in the schedule's time zone) is inside
// a window. An empty schedule allows any time.
func (sc Schedule) Allows(t time.Time) bool {
	if len(sc.Windows) == 0 {
		return true
	}
	day := int(t.Weekday())
	prev := (day + 6) % 7
	now := t.Hour()*60 + t.Minute()
	for _, w := range sc.Windows {
		start, _ := minutes(w.Start)
		end, _ := minutes(w.End)
		if start < end {
			if w.has(day) && now >= start && now < end {
				return true
			}
			continue
		}
		// Through midnight: the evening of a listed day, or the early
		// morning after one.
		if (w.has(day) && now >= start) || (w.has(prev) && now < end) {
			return true
		}
	}
	return false
}

// Next returns the next moment after t that a window opens, or false if the
// schedule has no windows.
func (sc Schedule) Next(t time.Time) (time.Time, bool) {
	var best time.Time
	for _, w := range sc.Windows {
		start, _ := minutes(w.Start)
		for add := 0; add <= 7; add++ {
			d := t.AddDate(0, 0, add)
			if !w.has(int(d.Weekday())) {
				continue
			}
			c := time.Date(d.Year(), d.Month(), d.Day(), start/60, start%60, 0, 0, t.Location())
			if c.After(t) && (best.IsZero() || c.Before(best)) {
				best = c
			}
		}
	}
	return best, !best.IsZero()
}

// Describe writes the schedule for people, e.g. "seg, ter, qua das 08:00 às
// 18:00; sáb das 09:00 às 12:00".
func (sc Schedule) Describe() string {
	if len(sc.Windows) == 0 {
		return "a qualquer hora"
	}
	parts := make([]string, 0, len(sc.Windows))
	for _, w := range sc.Windows {
		names := make([]string, 0, len(w.Days))
		for _, d := range w.Days {
			names = append(names, weekdayNames[d])
		}
		parts = append(parts, fmt.Sprintf("%s das %s às %s", strings.Join(names, ", "), w.Start, w.End))
	}
	return strings.Join(parts, "; ")
}
