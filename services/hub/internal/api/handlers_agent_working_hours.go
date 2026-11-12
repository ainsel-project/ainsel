package api

import (
	"strings"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// Working hours withhold task delivery on a weekly schedule. The hub holds the one
// dispatch decision there is: while an agent is outside its window the queue signal
// carries no due-task ids (see queuesignal.WithGate), so the operator creates no
// task Job and no pod starts. The tasks pile up as a backlog - the queue-depth
// fields show what is waiting - and drain in id order when the window opens.
//
// Pause and working hours are two axes of the same question ("may this agent be
// handed work?") and either one holds the queue: pause is a human pressing a
// button, working hours is the schedule the agent's people keep. Unlike pause there
// is no manual lift - the window is what ends the hold.
//
// Nothing is exempt. A chat message sent at 23:00 to an agent configured for
// 09:00-17:00 is stored, queued, and answered when the window opens. That is the
// honest consequence of a schedule: an exemption for one kind of event would make
// the window a preference. The agent response carries hoursOpen and
// hoursNextOpenAt so a console can say so out loud instead of looking broken.

// AgentWorkingHoursInfo is the console-facing form of an agent's working hours: the
// weekly window inside which the hub may hand it work.
type AgentWorkingHoursInfo struct {
	// Timezone is the IANA zone the window is written in, e.g. "Europe/Berlin", so
	// "09:00" means 09:00 there rather than 09:00 UTC. Empty on read means UTC.
	Timezone string `json:"timezone,omitempty"`
	// Days are the weekday numbers the window repeats on, numbered as in cron: 0 and
	// 7 are Sunday, 1 is Monday through 6 Saturday. Empty means every day.
	Days []int32 `json:"days,omitempty"`
	// Start and End are "HH:MM" wall-clock times. An End earlier than Start is one
	// window that closes on the following day (22:00-06:00 is a night shift).
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

// blank reports whether the request carried a window with nothing in it, which is
// how a client asks for the window to be removed. It is the same patch convention
// as Env: present-but-empty clears, absent means leave unchanged.
func (w *AgentWorkingHoursInfo) blank() bool {
	return w == nil || (strings.TrimSpace(w.Timezone) == "" &&
		len(w.Days) == 0 &&
		strings.TrimSpace(w.Start) == "" &&
		strings.TrimSpace(w.End) == "")
}

// toSpecWorkingHours converts the request form to the CRD form, trimming the
// whitespace a hand-written JSON body picks up. Timezone is left as sent: an empty
// one reads as UTC, and the API server defaults it to "UTC" on write anyway.
func (w *AgentWorkingHoursInfo) toSpecWorkingHours() *agentv1alpha1.AgentWorkingHours {
	if w == nil {
		return nil
	}
	return &agentv1alpha1.AgentWorkingHours{
		Timezone: strings.TrimSpace(w.Timezone),
		Days:     append([]int32(nil), w.Days...),
		Start:    strings.TrimSpace(w.Start),
		End:      strings.TrimSpace(w.End),
	}
}

// workingHoursProblem explains a window the hub cannot evaluate, or returns "" when
// it is usable. The rule is the evaluator's own (see shared/api's Validate), so the
// console cannot accept a window that the delivery paths would then fail open on.
func workingHoursProblem(wh *agentv1alpha1.AgentWorkingHours) string {
	if err := wh.Validate(); err != nil {
		return err.Error()
	}
	return ""
}

// workingHoursFromSpec renders the CRD form for a response. Nil means the agent may
// run at any hour.
func workingHoursFromSpec(wh *agentv1alpha1.AgentWorkingHours) *AgentWorkingHoursInfo {
	if wh == nil {
		return nil
	}
	return &AgentWorkingHoursInfo{
		Timezone: wh.Timezone,
		Days:     append([]int32(nil), wh.Days...),
		Start:    wh.Start,
		End:      wh.End,
	}
}
