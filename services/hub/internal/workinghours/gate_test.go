package workinghours

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// berlin is a real zone rather than a fixed offset, because the whole point of
// the feature is that a window is wall-clock time somewhere.
var berlin = func() *time.Location {
	l, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		// No embedded tzdata: every window assertion below would be meaningless.
		panic("working hours tests need time/tzdata: " + err.Error())
	}
	return l
}()

// A fixed Monday: 12:00 Berlin is inside a Mon-Fri 09:00-18:00 window, 20:00 is
// outside it, and the window reopens on Tuesday at 09:00.
var (
	insideWindow  = time.Date(2026, 3, 9, 12, 0, 0, 0, berlin)
	outsideWindow = time.Date(2026, 3, 9, 20, 0, 0, 0, berlin)
	nextOpen      = time.Date(2026, 3, 10, 9, 0, 0, 0, berlin)
)

func agentWithName(name string, wh *agentv1alpha1.AgentWorkingHours) *agentv1alpha1.Agent {
	return &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentSpec{
			DisplayName:  name,
			ImageRef:     agentv1alpha1.AgentImageRef{Name: "img"},
			WorkingHours: wh,
		},
	}
}

func gateAt(t *testing.T, now time.Time, objs ...runtime.Object) *Gate {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := agentv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	g := New(fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build(), "test-ns")
	g.now = func() time.Time { return now }
	return g
}

func window(tz string, days []int32, start, end string) *agentv1alpha1.AgentWorkingHours {
	return &agentv1alpha1.AgentWorkingHours{Timezone: tz, Days: days, Start: start, End: end}
}

var weekdays = []int32{1, 2, 3, 4, 5}

func TestGateHold(t *testing.T) {
	agent := agentWithName("olli", window("Europe/Berlin", weekdays, "09:00", "18:00"))

	held, opens := gateAt(t, insideWindow, agent).Hold(context.Background(), "olli")
	if held {
		t.Errorf("inside the window: held = true (opens %s), want false", opens)
	}

	held, opens = gateAt(t, outsideWindow, agent).Hold(context.Background(), "olli")
	if !held {
		t.Fatal("outside the window: held = false, want true")
	}
	if !opens.Equal(nextOpen) {
		t.Errorf("opens at %s, want %s", opens.UTC(), nextOpen.UTC())
	}
}

// An agent with no window is the common case and must cost the caller nothing but
// a lookup: never held, no opening time.
func TestGateNoWindowNeverHolds(t *testing.T) {
	agent := agentWithName("olli", nil)
	held, opens := gateAt(t, outsideWindow, agent).Hold(context.Background(), "olli")
	if held || !opens.IsZero() {
		t.Errorf("held = %v opens = %s, want false/zero", held, opens)
	}
}

// Unknown agents and a dead API server fail open. The claim path must keep
// working through an API-server hiccup; holding the queue is not the fallback for
// a lookup that failed.
func TestGateFailsOpen(t *testing.T) {
	if held, _ := gateAt(t, outsideWindow).Hold(context.Background(), "nobody"); held {
		t.Error("missing agent: held = true, want false")
	}

	broken := agentWithName("difi", window("Mars/Olympus", weekdays, "09:00", "18:00"))
	if held, _ := gateAt(t, outsideWindow, broken).Hold(context.Background(), "difi"); held {
		t.Error("unreadable window: held = true, want false (a typo must not stall the queue forever)")
	}
}

// A nil client is what a hub without a k8s reader hands in; the gate must read as
// "no window configured" rather than panic.
func TestGateNilClient(t *testing.T) {
	if held, _ := (&Gate{}).Hold(context.Background(), "olli"); held {
		t.Error("nil client: held = true, want false")
	}
	if held, _ := New(nil, "test-ns").Hold(context.Background(), "olli"); held {
		t.Error("nil client: held = true, want false")
	}
	if held, _ := gateAt(t, outsideWindow).Hold(context.Background(), ""); held {
		t.Error("empty agent name: held = true, want false")
	}
}
