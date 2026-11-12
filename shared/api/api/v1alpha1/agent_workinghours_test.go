package v1alpha1

import (
	"testing"
	"time"
)

// loc loads an IANA zone, failing the test rather than falling back to UTC - a
// silently wrong zone makes every window assertion below meaningless.
func loc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return l
}

// at builds a wall-clock time in the zone, which is how a working-hours
// configuration is written.
func at(t *testing.T, l *time.Location, wall string) time.Time {
	t.Helper()
	got, err := time.ParseInLocation("2006-01-02 15:04", wall, l)
	if err != nil {
		t.Fatalf("parse %q: %v", wall, err)
	}
	return got
}

func hours(tz string, days []int32, start, end string) *AgentWorkingHours {
	return &AgentWorkingHours{Timezone: tz, Days: days, Start: start, End: end}
}

// weekdays is the Mon-Fri set, the shape most agents will be configured with.
var weekdays = []int32{1, 2, 3, 4, 5}

func TestWorkingHoursState_WorkdayWindow(t *testing.T) {
	berlin := loc(t, "Europe/Berlin")
	a := &Agent{Spec: AgentSpec{WorkingHours: hours("Europe/Berlin", weekdays, "09:00", "18:00")}}

	cases := []struct {
		wall     string
		wantOpen bool
	}{
		{"2026-03-09 08:59", false}, // Monday, one minute before it opens
		{"2026-03-09 09:00", true},  // opens exactly at Start
		{"2026-03-09 17:59", true},
		{"2026-03-09 18:00", false}, // closes exactly at End
		{"2026-03-09 23:00", false},
		{"2026-03-14 12:00", false}, // Saturday
		{"2026-03-15 12:00", false}, // Sunday
	}
	for _, c := range cases {
		now := at(t, berlin, c.wall)
		open, _, err := a.WorkingHoursState(now)
		if err != nil {
			t.Fatalf("%s: WorkingHoursState: %v", c.wall, err)
		}
		if open != c.wantOpen {
			t.Errorf("%s: open = %v, want %v", c.wall, open, c.wantOpen)
		}
	}
}

// The window is written in the agent's zone, not the process's. A hub that runs
// in UTC (every distroless pod does) must still hold work until 09:00 Berlin.
func TestWorkingHoursState_ZoneBeatsProcessZone(t *testing.T) {
	berlin := loc(t, "Europe/Berlin")
	a := &Agent{Spec: AgentSpec{WorkingHours: hours("Europe/Berlin", weekdays, "09:00", "18:00")}}

	// 08:00 in Berlin is 07:00 UTC in winter: still closed, read from a UTC clock.
	now := at(t, berlin, "2026-01-12 08:00").In(time.UTC)
	open, next, err := a.WorkingHoursState(now)
	if err != nil {
		t.Fatalf("WorkingHoursState: %v", err)
	}
	if open {
		t.Fatalf("%s: open = true, want false (the window is Berlin wall-clock)", now)
	}
	if want := at(t, berlin, "2026-01-12 09:00"); !next.Equal(want) {
		t.Errorf("next open = %s, want %s", next.UTC(), want.UTC())
	}
}

// Across the spring transition Berlin loses 02:00-03:00. Windows are calendar
// windows, so the hour must be lost from absolute time, not from the wall clock.
func TestWorkingHoursState_DSTSpring(t *testing.T) {
	berlin := loc(t, "Europe/Berlin")
	a := &Agent{Spec: AgentSpec{WorkingHours: hours("Europe/Berlin", weekdays, "09:00", "18:00")}}

	// Sunday 2026-03-29 is the switch. The working days either side read 09:00 on
	// the wall clock and are 71 hours apart in absolute time.
	before := at(t, berlin, "2026-03-27 09:00")
	after := at(t, berlin, "2026-03-30 09:00")
	if d := after.Sub(before); d != 71*time.Hour {
		t.Fatalf("expected the lost DST hour to make Monday 09:00 exactly 71h after Friday 09:00, got %v", d)
	}
	for _, now := range []time.Time{before, after} {
		open, _, err := a.WorkingHoursState(now)
		if err != nil {
			t.Fatalf("%s: %v", now, err)
		}
		if !open {
			t.Errorf("%s: open = false, want true", now)
		}
	}

	// The switch night itself: Sunday 01:30 is inside no window, and the next open
	// is Monday 09:00 local - the skipped hour must not shift the answer.
	open, next, err := a.WorkingHoursState(at(t, berlin, "2026-03-29 01:30"))
	if err != nil {
		t.Fatalf("WorkingHoursState: %v", err)
	}
	if open {
		t.Error("Sunday 01:30 read open, want closed (Sunday is not a working day)")
	}
	if want := at(t, berlin, "2026-03-30 09:00"); !next.Equal(want) {
		t.Errorf("next open = %s, want %s", next.UTC(), want.UTC())
	}
}

// A night shift (22:00-06:00) is one window, not two: work handed to the agent at
// 23:00 must run, and so must work handed over at 02:00 the next morning.
func TestWorkingHoursState_CrossesMidnight(t *testing.T) {
	berlin := loc(t, "Europe/Berlin")
	a := &Agent{Spec: AgentSpec{WorkingHours: hours("Europe/Berlin", weekdays, "22:00", "06:00")}}

	cases := []struct {
		wall     string
		wantOpen bool
	}{
		{"2026-03-09 21:59", false}, // Monday, before Monday's window starts
		{"2026-03-09 22:00", true},
		{"2026-03-10 02:00", true}, // Tuesday 02:00 is Monday's window, still open
		{"2026-03-10 05:59", true},
		{"2026-03-10 06:00", false}, // Monday's window ends
		{"2026-03-10 12:00", false}, // Tuesday daytime
		{"2026-03-10 22:00", true},  // Tuesday's window
		{"2026-03-13 23:00", true},  // Friday's window, running into Saturday
		{"2026-03-14 03:00", true},  // Saturday 03:00 belongs to Friday's window
		{"2026-03-14 23:00", false}, // Saturday's window does not exist
		{"2026-03-15 03:00", false}, // Sunday 03:00 would need Saturday to be a working day
	}
	for _, c := range cases {
		now := at(t, berlin, c.wall)
		open, _, err := a.WorkingHoursState(now)
		if err != nil {
			t.Fatalf("%s: %v", c.wall, err)
		}
		if open != c.wantOpen {
			t.Errorf("%s: open = %v, want %v", c.wall, open, c.wantOpen)
		}
	}
}

// Friday 23:00 is inside Friday's night window, so the next change is its close at
// Saturday 06:00 - and from Saturday noon the next open is Monday 22:00, not "in
// five minutes".
func TestWorkingHoursState_NextChange(t *testing.T) {
	berlin := loc(t, "Europe/Berlin")
	a := &Agent{Spec: AgentSpec{WorkingHours: hours("Europe/Berlin", weekdays, "22:00", "06:00")}}

	open, next, err := a.WorkingHoursState(at(t, berlin, "2026-03-13 23:00"))
	if err != nil {
		t.Fatalf("WorkingHoursState: %v", err)
	}
	if !open {
		t.Fatal("Friday 23:00 read closed, want open")
	}
	if want := at(t, berlin, "2026-03-14 06:00"); !next.Equal(want) {
		t.Errorf("next change = %s, want %s (the close)", next.UTC(), want.UTC())
	}

	open, next, err = a.WorkingHoursState(at(t, berlin, "2026-03-14 12:00"))
	if err != nil {
		t.Fatalf("WorkingHoursState: %v", err)
	}
	if open {
		t.Fatal("Saturday 12:00 read open, want closed")
	}
	if want := at(t, berlin, "2026-03-16 22:00"); !next.Equal(want) {
		t.Errorf("next open = %s, want %s", next.UTC(), want.UTC())
	}
}

// No days means every day; a one-day list means exactly that day.
func TestWorkingHoursState_Days(t *testing.T) {
	berlin := loc(t, "Europe/Berlin")

	all := &Agent{Spec: AgentSpec{WorkingHours: hours("Europe/Berlin", nil, "09:00", "18:00")}}
	open, _, err := all.WorkingHoursState(at(t, berlin, "2026-03-14 12:00"))
	if err != nil {
		t.Fatalf("WorkingHoursState: %v", err)
	}
	if !open {
		t.Error("empty Days read closed on a Saturday, want open (empty = every day)")
	}

	mondayOnly := &Agent{Spec: AgentSpec{WorkingHours: hours("Europe/Berlin", []int32{1}, "09:00", "18:00")}}
	for wall, want := range map[string]bool{
		"2026-03-09 12:00": true,  // Monday
		"2026-03-10 12:00": false, // Tuesday
		"2026-03-16 12:00": true,  // next Monday
	} {
		open, _, err := mondayOnly.WorkingHoursState(at(t, berlin, wall))
		if err != nil {
			t.Fatalf("%s: %v", wall, err)
		}
		if open != want {
			t.Errorf("%s: open = %v, want %v", wall, open, want)
		}
	}
}

// An agent with no workingHours block is always open: the field is opt-in, and
// every agent deployed before it existed must keep running around the clock.
func TestWorkingHoursState_NoBlockAlwaysOpen(t *testing.T) {
	a := &Agent{}
	open, next, err := a.WorkingHoursState(time.Date(2026, 3, 15, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("WorkingHoursState: %v", err)
	}
	if !open {
		t.Error("open = false, want true (no workingHours configured)")
	}
	if !next.IsZero() {
		t.Errorf("next change = %s, want zero for an always-open agent", next)
	}
}

// Sunday is written 7 out of cron habit and 0 out of Go's time.Weekday. Both must
// read as Sunday, or a list that means "Mon-Sun" silently holds nothing.
func TestWorkingHoursState_DayNumbering(t *testing.T) {
	berlin := loc(t, "Europe/Berlin")
	sundayNoon := at(t, berlin, "2026-03-15 12:00")

	for _, days := range [][]int32{{0}, {7}} {
		a := &Agent{Spec: AgentSpec{WorkingHours: hours("Europe/Berlin", days, "09:00", "18:00")}}
		open, _, err := a.WorkingHoursState(sundayNoon)
		if err != nil {
			t.Fatalf("days %v: %v", days, err)
		}
		if !open {
			t.Errorf("days %v: Sunday read closed, want open", days)
		}
	}

	// A Mon-Sun list written 0..6 must cover Monday too (0 is Sunday, 1 is Monday).
	a := &Agent{Spec: AgentSpec{WorkingHours: hours("Europe/Berlin", []int32{0, 1, 2, 3, 4, 5, 6}, "09:00", "18:00")}}
	open, _, err := a.WorkingHoursState(at(t, berlin, "2026-03-09 12:00"))
	if err != nil {
		t.Fatalf("WorkingHoursState: %v", err)
	}
	if !open {
		t.Error("0..6 list: Monday read closed, want open")
	}
}

// A bad timezone or a zero-length window is a configuration error, not a
// permission to run or a reason to hold: it is reported, so the caller can decide
// and say so.
func TestWorkingHoursState_Errors(t *testing.T) {
	cases := []struct {
		name  string
		hours *AgentWorkingHours
	}{
		{"unknown zone", hours("Mars/Olympus", weekdays, "09:00", "18:00")},
		{"start equals end", hours("UTC", weekdays, "09:00", "09:00")},
		{"malformed start", hours("UTC", weekdays, "9am", "18:00")},
		{"malformed end", hours("UTC", weekdays, "09:00", "24:00")},
		{"day out of range", hours("UTC", []int32{8}, "09:00", "18:00")},
		{"negative day", hours("UTC", []int32{-1}, "09:00", "18:00")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Agent{Spec: AgentSpec{WorkingHours: c.hours}}
			if _, _, err := a.WorkingHoursState(time.Now()); err == nil {
				t.Fatal("WorkingHoursState returned no error, want a configuration error")
			}
			if err := c.hours.Validate(); err == nil {
				t.Fatal("Validate returned no error, want a configuration error")
			}
		})
	}
}

// Empty Timezone reads as UTC rather than failing: the CRD defaults it, so an
// empty value only reaches here from a client that bypassed defaulting.
func TestWorkingHoursState_EmptyTimezoneIsUTC(t *testing.T) {
	a := &Agent{Spec: AgentSpec{WorkingHours: hours("", weekdays, "09:00", "18:00")}}
	open, _, err := a.WorkingHoursState(time.Date(2026, 3, 9, 9, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("WorkingHoursState: %v", err)
	}
	if !open {
		t.Error("empty Timezone read closed at 09:30 UTC, want open (empty = UTC)")
	}
}

// A valid window must pass Validate, including the wrap case the CRD schema
// cannot express.
func TestValidate_AcceptsUsableWindows(t *testing.T) {
	for _, w := range []*AgentWorkingHours{
		hours("UTC", nil, "00:00", "23:59"),
		hours("Europe/Berlin", weekdays, "22:00", "06:00"),
		hours("America/New_York", []int32{6, 7}, "08:30", "17:30"),
	} {
		if err := w.Validate(); err != nil {
			t.Errorf("%s-%s in %s: Validate: %v", w.Start, w.End, w.Timezone, err)
		}
	}
}
