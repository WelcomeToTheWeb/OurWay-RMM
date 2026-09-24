package session

import (
	"context"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/welcometotheweb/ourway-rmm/proto/gen/ourway-rmm/agent/v1"
)

// mockInputRelayer records input events for test assertions.
type mockInputRelayer struct {
	mu          sync.Mutex
	mouse       []mockMouseEvent
	keyboard    []mockKeyboardEvent
	mouseErr    error
	keyboardErr error
}

type mockMouseEvent struct {
	Type       string
	X, Y       int
	Button     int
	WheelDelta int
}

type mockKeyboardEvent struct {
	Type      string
	Codepoint uint32
	Modifiers uint32
}

func (m *mockInputRelayer) MouseEvent(ctx context.Context, eventType string, x, y, button, wheelDelta int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mouse = append(m.mouse, mockMouseEvent{
		Type: eventType, X: x, Y: y, Button: button, WheelDelta: wheelDelta,
	})
	return m.mouseErr
}

func (m *mockInputRelayer) KeyboardEvent(ctx context.Context, eventType string, codepoint uint32, modifiers uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keyboard = append(m.keyboard, mockKeyboardEvent{
		Type: eventType, Codepoint: codepoint, Modifiers: modifiers,
	})
	return m.keyboardErr
}

func (m *mockInputRelayer) Close() error { return nil }

func (m *mockInputRelayer) mouseEvents() []mockMouseEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mouse
}

func (m *mockInputRelayer) keyboardEvents() []mockKeyboardEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keyboard
}

// TestDriverInputRouting verifies that the session driver routes
// mouse and keyboard events from downlink frames to the input relayer —
// but only for the device's live session (see the gating tests below).
func TestDriverInputRouting(t *testing.T) {
	var sentFrames []*agentv1.SessionFrame
	d := NewDriver(DriverConfig{
		SendFrame: func(ctx context.Context, f *agentv1.SessionFrame) error {
			sentFrames = append(sentFrames, f)
			return nil
		},
		NewCapturer: func() (Capturer, error) {
			return newTestCapturer(), nil
		},
	})
	d.input = &mockInputRelayer{}

	ctx := context.Background()

	// Input is only relayed for a live session: open one first.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_Open{
			Open: &agentv1.SessionControl_OpenSession{SessionId: "test-session", Fps: 1},
		},
	})

	// Send a mouse move event.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_MouseEvent_{
			MouseEvent: &agentv1.SessionControl_MouseEvent{
				SessionId:  "test-session",
				EventType:  "move",
				X:          100,
				Y:          200,
				Button:     0,
				WheelDelta: 0,
			},
		},
	})

	// Send a mouse down event.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_MouseEvent_{
			MouseEvent: &agentv1.SessionControl_MouseEvent{
				SessionId:  "test-session",
				EventType:  "down",
				X:          100,
				Y:          200,
				Button:     0,
				WheelDelta: 0,
			},
		},
	})

	// Send a mouse up event.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_MouseEvent_{
			MouseEvent: &agentv1.SessionControl_MouseEvent{
				SessionId:  "test-session",
				EventType:  "up",
				X:          100,
				Y:          200,
				Button:     0,
				WheelDelta: 0,
			},
		},
	})

	// Send a keyboard event.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_KeyboardEvent_{
			KeyboardEvent: &agentv1.SessionControl_KeyboardEvent{
				SessionId: "test-session",
				EventType: "down",
				Codepoint: 65,
				Modifiers: 0,
			},
		},
	})

	// Verify events were relayed.
	relayer := d.input.(*mockInputRelayer)
	mouseEvents := relayer.mouseEvents()
	keyboardEvents := relayer.keyboardEvents()

	if len(mouseEvents) != 3 {
		t.Fatalf("expected 3 mouse events, got %d", len(mouseEvents))
	}
	if len(keyboardEvents) != 1 {
		t.Fatalf("expected 1 keyboard event, got %d", len(keyboardEvents))
	}

	// Verify first event was a move.
	if mouseEvents[0].Type != "move" || mouseEvents[0].X != 100 || mouseEvents[0].Y != 200 {
		t.Errorf("unexpected first mouse event: %+v", mouseEvents[0])
	}

	// Verify keyboard event.
	if keyboardEvents[0].Codepoint != 65 || keyboardEvents[0].Modifiers != 0 {
		t.Errorf("unexpected keyboard event: %+v", keyboardEvents[0])
	}

	d.Stop()
}

// TestDriverInputRelayerErrors verifies that relayer errors are logged, not
// propagated (so a failed event doesn't stop the control loop).
func TestDriverInputRelayerErrors(t *testing.T) {
	relayer := &mockInputRelayer{mouseErr: context.DeadlineExceeded, keyboardErr: context.DeadlineExceeded}
	d := NewDriver(DriverConfig{
		SendFrame:   func(ctx context.Context, f *agentv1.SessionFrame) error { return nil },
		NewCapturer: func() (Capturer, error) { return newTestCapturer(), nil },
	})
	d.input = relayer

	ctx := context.Background()

	// Events must target the live session to reach the relayer.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_Open{
			Open: &agentv1.SessionControl_OpenSession{SessionId: "s1", Fps: 1},
		},
	})

	// Fire both types of events — the driver must not panic or stop.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_MouseEvent_{
			MouseEvent: &agentv1.SessionControl_MouseEvent{SessionId: "s1", EventType: "move", X: 10, Y: 10},
		},
	})
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_KeyboardEvent_{
			KeyboardEvent: &agentv1.SessionControl_KeyboardEvent{SessionId: "s1", EventType: "down", Codepoint: 65},
		},
	})

	// Events should still be recorded despite the error.
	if len(relayer.mouseEvents()) != 1 {
		t.Errorf("expected mouse event to be recorded despite error")
	}
	if len(relayer.keyboardEvents()) != 1 {
		t.Errorf("expected keyboard event to be recorded despite error")
	}

	d.Stop()
}

// TestDriverInputRelayerNil verifies that events are silently dropped
// when no input relayer is configured (phase 1 view-only operation).
func TestDriverInputRelayerNil(t *testing.T) {
	d := NewDriver(DriverConfig{
		SendFrame:   func(ctx context.Context, f *agentv1.SessionFrame) error { return nil },
		NewCapturer: func() (Capturer, error) { return newTestCapturer(), nil },
	})
	// No input relayer set.

	ctx := context.Background()

	// Fire events — they should be dropped, not panic.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_MouseEvent_{
			MouseEvent: &agentv1.SessionControl_MouseEvent{EventType: "move", X: 10, Y: 10},
		},
	})
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_KeyboardEvent_{
			KeyboardEvent: &agentv1.SessionControl_KeyboardEvent{EventType: "down", Codepoint: 65},
		},
	})

	// Just verify no panic occurred; nothing should have been recorded.
	d.Stop()
}

// TestClampFPS ensures the fps hint is clamped to valid range.
func TestClampFPS(t *testing.T) {
	tests := []struct {
		in, out int
	}{
		{0, DefaultFPS},
		{-5, DefaultFPS},
		{1, 1},
		{15, 15},
		{30, 30},
		{60, MaxFPS},
	}
	for _, tc := range tests {
		if got := clampFPS(tc.in); got != tc.out {
			t.Errorf("clampFPS(%d) = %d, want %d", tc.in, got, tc.out)
		}
	}
}

// TestDriverInputRequiresLiveSession verifies the input gate: events for a
// closed (none), missing-id or superseded session are dropped; only the
// live session's id is relayed. A compromised/buggy server cannot drive the
// desktop outside an operator-initiated session.
func TestDriverInputRequiresLiveSession(t *testing.T) {
	relayer := &mockInputRelayer{}
	d := NewDriver(DriverConfig{
		SendFrame:   func(ctx context.Context, f *agentv1.SessionFrame) error { return nil },
		NewCapturer: func() (Capturer, error) { return newTestCapturer(), nil },
	})
	d.input = relayer
	ctx := context.Background()

	mouse := func(sessionID string) {
		d.Control(ctx, &agentv1.SessionControl{
			Action: &agentv1.SessionControl_MouseEvent_{
				MouseEvent: &agentv1.SessionControl_MouseEvent{SessionId: sessionID, EventType: "move", X: 1, Y: 2},
			},
		})
	}

	// No live session: dropped.
	mouse("s1")
	mouse("")
	if n := len(relayer.mouseEvents()); n != 0 {
		t.Fatalf("events relayed without a live session: %d", n)
	}

	// Open s1: events for s1 relayed, s2 dropped.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_Open{
			Open: &agentv1.SessionControl_OpenSession{SessionId: "s1", Fps: 1},
		},
	})
	mouse("s1")
	mouse("s2")
	if n := len(relayer.mouseEvents()); n != 1 {
		t.Fatalf("expected exactly 1 relayed event (s1 only), got %d", n)
	}

	// Close: everything dropped again.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_Close{
			Close: &agentv1.SessionControl_CloseSession{SessionId: "s1"},
		},
	})
	mouse("s1")
	if n := len(relayer.mouseEvents()); n != 1 {
		t.Fatalf("event relayed after close, got %d events", n)
	}
	d.Stop()
}

// TestDriverBlockedCombo verifies dangerous key combos (Win+L lock, Win+R
// run dialog) are never relayed — whether the modifier is inline in the key
// event or arrives as the protocol's separate modifier-key event.
func TestDriverBlockedCombo(t *testing.T) {
	relayer := &mockInputRelayer{}
	d := NewDriver(DriverConfig{
		SendFrame:   func(ctx context.Context, f *agentv1.SessionFrame) error { return nil },
		NewCapturer: func() (Capturer, error) { return newTestCapturer(), nil },
	})
	d.input = relayer
	ctx := context.Background()
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_Open{
			Open: &agentv1.SessionControl_OpenSession{SessionId: "s1", Fps: 1},
		},
	})

	key := func(eventType string, codepoint uint32, modifiers uint32) {
		d.Control(ctx, &agentv1.SessionControl{
			Action: &agentv1.SessionControl_KeyboardEvent_{
				KeyboardEvent: &agentv1.SessionControl_KeyboardEvent{
					SessionId: "s1", EventType: eventType, Codepoint: codepoint, Modifiers: modifiers,
				},
			},
		})
	}

	// Inline modifier: Win+L in one event.
	key("down", 76, 8) // L with meta held
	key("up", 76, 8)
	// Separate modifier events: meta down, then R.
	key("down", 0, 8) // meta down
	key("down", 82, 0) // R while meta tracked as held
	key("up", 82, 0)
	key("up", 0, 8) // meta up

	if n := len(relayer.keyboardEvents()); n != 0 {
		t.Fatalf("blocked combos relayed: %d events %v", n, relayer.keyboardEvents())
	}

	// A plain L (no meta) is fine.
	key("down", 76, 0)
	key("up", 76, 0)
	if n := len(relayer.keyboardEvents()); n != 2 {
		t.Fatalf("plain key dropped: expected 2 events, got %d", n)
	}
	d.Stop()
}

// TestDriverZombieClearOnSendFailure verifies the liveness fix: when the
// capture loop's frame send fails (stream dropped), the driver must clear
// the live session so a re-sent open (reconnect) starts a fresh loop instead
// of fast-pathing into the dead record (the old behavior left the viewer
// frozen forever).
func TestDriverZombieClearOnSendFailure(t *testing.T) {
	var mu sync.Mutex
	sendErr := false
	d := NewDriver(DriverConfig{
		SendFrame: func(ctx context.Context, f *agentv1.SessionFrame) error {
			mu.Lock()
			defer mu.Unlock()
			if sendErr {
				return context.Canceled // simulate a dead stream
			}
			return nil
		},
		NewCapturer: func() (Capturer, error) { return newTestCapturer(), nil },
	})
	ctx := context.Background()

	// Open at a high fps so the first tick (and send failure) is fast.
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_Open{
			Open: &agentv1.SessionControl_OpenSession{SessionId: "s1", Fps: MaxFPS},
		},
	})
	if d.ActiveSessionID() != "s1" {
		t.Fatalf("session not open: %q", d.ActiveSessionID())
	}

	// Kill the stream: the loop's next send fails and it must exit, clearing
	// the live session.
	mu.Lock()
	sendErr = true
	mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for d.ActiveSessionID() != "" {
		if time.Now().After(deadline) {
			t.Fatalf("live session not cleared after send failure (zombie): %q", d.ActiveSessionID())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A re-open with the SAME session id must start a fresh loop (the old
	// code fast-pathed on the dead record and captured nothing again).
	sendErr = false // stream re-established
	d.Control(ctx, &agentv1.SessionControl{
		Action: &agentv1.SessionControl_Open{
			Open: &agentv1.SessionControl_OpenSession{SessionId: "s1", Fps: 1},
		},
	})
	if d.ActiveSessionID() != "s1" {
		t.Fatalf("re-open after reconnect failed: %q", d.ActiveSessionID())
	}
	d.Stop()
}
