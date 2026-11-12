package v1alpha1

import (
	"fmt"
	"strings"
	"time"

	// The window is named as an IANA zone, so the zone database has to be
	// present. The hub, operator and console images are distroless and carry no
	// /usr/share/zoneinfo, where time.LoadLocation would otherwise fail and make
	// every configured window unreadable.
	_ "time/tzdata"
)

// Working hours are read the way a person reads a timesheet: as wall-clock time
// in the agent's zone, evaluated at read time. Like IsPaused this needs no
// timers - the same object reads open at 08:59 and closed at 18:00 - and unlike
// a timer it cannot be missed by a restarting hub.
//
// The cost of read-time evaluation is that the answer depends on the machine's
// notion of "now", so the zone has to be resolved per call. time.LoadLocation
// is cached by the runtime, and these lookups happen a handful of times per
// queue signal, not per event.

// maxDayScan bounds how far ahead WorkingHoursState looks for the next window.
// A week of candidate days is enough for any non-empty day set; the bound exists
// so a day set that matches nothing (impossible today, cheap to keep honest)
// cannot spin.
const maxDayScan = 8

// weekWindow is a resolved AgentWorkingHours: a zone, a set of days, and a
// window in minutes-from-midnight, with wrap saying the window closes the
// following day.
type weekWindow struct {
	loc              *time.Location
	days             [7]bool
	startMin, endMin int
	wrap             bool
}

// resolve turns the CRD form into an evaluable window, reporting the first
// configuration error. It is the single place that decides what a valid window
// is, so the hub's write path can reject a bad one with the same message the
// read path would fail open on.
func (w *AgentWorkingHours) resolve() (weekWindow, error) {
	if w == nil {
		return weekWindow{}, fmt.Errorf("workingHours: no window configured")
	}

	loc := time.UTC
	if tz := strings.TrimSpace(w.Timezone); tz != "" {
		var err error
		loc, err = time.LoadLocation(tz)
		if err != nil {
			return weekWindow{}, fmt.Errorf("workingHours: unknown timezone %q: %w", w.Timezone, err)
		}
	}

	var days [7]bool
	if len(w.Days) == 0 {
		for i := range days {
			days[i] = true
		}
	}
	for _, d := range w.Days {
		// Numbered as in this repo's cron schedules: 0 and 7 are Sunday, 1 is
		// Monday. Accepting both spellings of Sunday is the point - a list
		// written from a crontab and one written from Go's time.Weekday must
		// mean the same week.
		if d < 0 || d > 7 {
			return weekWindow{}, fmt.Errorf("workingHours: day %d is out of range 0-7 (0 and 7 are Sunday)", d)
		}
		days[int(d)%7] = true
	}

	startMin, err := parseClock("start", w.Start)
	if err != nil {
		return weekWindow{}, err
	}
	endMin, err := parseClock("end", w.End)
	if err != nil {
		return weekWindow{}, err
	}
	switch {
	case startMin == endMin:
		return weekWindow{}, fmt.Errorf("workingHours: start %q equals end %q, which is no window at all", w.Start, w.End)
	case endMin < startMin:
		return weekWindow{loc: loc, days: days, startMin: startMin, endMin: endMin, wrap: true}, nil
	default:
		return weekWindow{loc: loc, days: days, startMin: startMin, endMin: endMin}, nil
	}
}

// parseClock reads a strict "HH:MM" wall-clock time into minutes from midnight.
// The CRD pattern rejects anything else; this is the same rule for callers that
// built the object in Go (hub API requests, tests, kubectl-less fakes).
func parseClock(field, v string) (int, error) {
	if len(v) != 5 || v[2] != ':' {
		return 0, fmt.Errorf("workingHours: %s %q is not HH:MM", field, v)
	}
	// Guarded by the length check above, so the byte indexes are safe.
	h, m := v[:2], v[3:]
	if !allDigits(h) || !allDigits(m) {
		return 0, fmt.Errorf("workingHours: %s %q is not HH:MM", field, v)
	}
	hours := int(h[0]-'0')*10 + int(h[1]-'0')
	mins := int(m[0]-'0')*10 + int(m[1]-'0')
	if hours > 23 || mins > 59 {
		return 0, fmt.Errorf("workingHours: %s %q is not a valid time of day", field, v)
	}
	return hours*60 + mins, nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// state reports whether t falls inside the window and when that answer next
// changes: while open, the instant the window closes; while closed, the instant
// it next opens. Both are absolute instants, so a caller can show or sleep to
// them without knowing about zones.
func (w weekWindow) state(t time.Time) (open bool, nextChange time.Time) {
	local := t.In(w.loc)
	// Start of today, on the calendar rather than by adding 24h: on a DST
	// transition day the previous day is 23 or 25 hours long, and a window is
	// defined by calendar dates, not by elapsed hours.
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, w.loc)

	// A window spans at most one calendar day plus its carry-over into the next,
	// so only today's and yesterday's windows can contain t.
	for _, off := range [...]int{0, -1} {
		day := today.AddDate(0, 0, off)
		if !w.days[int(day.Weekday())] {
			continue
		}
		openAt := minuteOfDay(day, w.startMin, w.loc)
		var closeAt time.Time
		if w.wrap {
			closeAt = minuteOfDay(day.AddDate(0, 0, 1), w.endMin, w.loc)
		} else {
			closeAt = minuteOfDay(day, w.endMin, w.loc)
		}
		// [openAt, closeAt): the opening minute is inside, the closing minute is
		// outside. Exactly one of the two edges belongs to the window, so two
		// back-to-back windows cannot both claim the boundary instant.
		if !t.Before(openAt) && t.Before(closeAt) {
			return true, closeAt
		}
	}

	// Closed: the next open is the first window start strictly after t, scanned
	// forward over whole days so a one-day-a-week window finds its day.
	for off := 0; off <= maxDayScan; off++ {
		day := today.AddDate(0, 0, off)
		if !w.days[int(day.Weekday())] {
			continue
		}
		if openAt := minuteOfDay(day, w.startMin, w.loc); openAt.After(t) {
			return false, openAt
		}
	}
	return false, time.Time{}
}

// minuteOfDay is the wall-clock time mins past midnight on day's date, in the
// window's zone. time.Date normalises a time the zone skipped (the hour lost to
// spring forward) onto the instant after it, which is the closest honest answer:
// a window that opens at 02:30 on that day opens at 03:30.
func minuteOfDay(day time.Time, mins int, loc *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, mins, 0, 0, loc)
}

// WorkingHoursState reports whether the agent may be handed work at now, and
// when that answer next changes (the close while open, the next open while
// closed). An agent with no workingHours block is always open, so every agent
// deployed before the field existed keeps running around the clock.
//
// A malformed window is returned as an error rather than as an open or closed
// answer: the caller has to decide whether a typo means "run anyway" or "hold the
// queue", and an invented default would be invisible. hub/internal/workinghours
// is that decision - it fails open and logs - so callers should go through it
// rather than each choosing their own.
func (a *Agent) WorkingHoursState(now time.Time) (open bool, nextChange time.Time, err error) {
	if a == nil || a.Spec.WorkingHours == nil {
		return true, time.Time{}, nil
	}
	w, err := a.Spec.WorkingHours.resolve()
	if err != nil {
		return false, time.Time{}, err
	}
	open, nextChange = w.state(now)
	return open, nextChange, nil
}

// Validate reports whether the window can be evaluated, for a hub write path
// that wants to reject a bad configuration instead of failing open on it later.
func (w *AgentWorkingHours) Validate() error {
	if w == nil {
		return nil
	}
	_, err := w.resolve()
	return err
}
