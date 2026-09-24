package sessionrelay

import (
	"encoding/base64"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/welcometotheweb/ourway-rmm/proto/gen/ourway-rmm/agent/v1"
)

func frame(sessionID string, jpeg []byte) *agentv1.SessionFrame {
	return &agentv1.SessionFrame{
		SessionId:   sessionID,
		Seq:         1,
		Codec:       "jpeg",
		Width:       640,
		Height:      480,
		Jpeg:        jpeg,
		CaptureTsMs: time.Now().UnixMilli(),
	}
}

// TestAutoCloseOnLastViewerDisconnect verifies the lifecycle fix: when the
// last browser viewer disconnects mid-session, the session is closed AND the
// OnAutoClose hook fires (main.go wires it to the close downlink so the
// agent stops capturing). A browser crash must not leave the screen
// streaming forever.
func TestAutoCloseOnLastViewerDisconnect(t *testing.T) {
	var mu sync.Mutex
	closed := false
	r := New()
	r.OnAutoClose = func(devID, sessionID string) {
		mu.Lock()
		defer mu.Unlock()
		closed = devID == "d1" && sessionID == "s1"
	}

	r.Open("d1", "s1", 10)
	viewer, cancel := r.Subscribe("d1")
	_ = viewer
	defer cancel()

	if id, ok := r.ActiveSession("d1"); !ok || id != "s1" {
		t.Fatalf("active session while viewer connected: %q %v", id, ok)
	}
	cancel() // last viewer leaves

	mu.Lock()
	ok := closed
	mu.Unlock()
	if !ok {
		t.Fatal("OnAutoClose did not fire for the active session")
	}
	if _, ok := r.ActiveSession("d1"); ok {
		t.Fatal("session still active after last viewer disconnect")
	}
	if _, ok := r.LatestFrame("d1"); ok {
		t.Fatal("device state not cleaned after last viewer disconnect")
	}
}

// TestStaleAndClosedFramesDropped: a frame must never (re)create or change a
// session — a rogue/replayed frame with an operator's session id used to
// implicitly open a capture session (no operator intent).
func TestStaleAndClosedFramesDropped(t *testing.T) {
	r := New()

	// No session at all: frames are dropped, no state created.
	r.OnFrame("d1", frame("s1", []byte("ghost")))
	if _, ok := r.LatestFrame("d1"); ok {
		t.Fatal("frame created session state with no Open")
	}
	if _, ok := r.ActiveSession("d1"); ok {
		t.Fatal("frame opened a session")
	}

	// Open s1: a frame for s0 (superseded) is dropped.
	r.Open("d1", "s1", 10)
	r.OnFrame("d1", frame("s0", []byte("stale")))
	if f, ok := r.LatestFrame("d1"); ok {
		t.Fatalf("stale-session frame stored: %q", f.JPEGB64)
	}

	// The active session's frames are stored.
	r.OnFrame("d1", frame("s1", []byte("live")))
	if f, ok := r.LatestFrame("d1"); !ok || f.JPEGB64 != base64.StdEncoding.EncodeToString([]byte("live")) {
		t.Fatalf("active-session frame not stored: %+v", f)
	}

	// Close: late frames for s1 are dropped (close downlink races in-flight
	// frames).
	r.Close("d1", "s1")
	r.OnFrame("d1", frame("s1", []byte("revive?")))
	if f, ok := r.LatestFrame("d1"); ok {
		t.Fatalf("closed session revived by a frame: %q", f.JPEGB64)
	}
}

// TestSubscribeReceivesLatestFrameInHello: a late-joining viewer gets the
// current frame immediately (right after the hello) instead of a blank
// canvas until the next capture tick.
func TestSubscribeReceivesLatestFrameInHello(t *testing.T) {
	r := New()
	r.Open("d1", "s1", 10)
	r.OnFrame("d1", frame("s1", []byte("frame-bytes")))

	viewer, cancel := r.Subscribe("d1")
	defer cancel()

	hello, ok := <-viewer.Chan()
	if !ok || hello.Kind != "hello" {
		t.Fatalf("no hello event on subscribe: %+v", hello)
	}
	evt, ok := <-viewer.Chan()
	if !ok {
		t.Fatal("no latest frame after hello")
	}
	if evt.Kind != "frame" || evt.JPEGB64 != base64.StdEncoding.EncodeToString([]byte("frame-bytes")) {
		t.Fatalf("latest frame not delivered to late joiner: %+v", evt)
	}
}

// TestOnAgentOfflineMarksSession: when the agent stream drops mid-session,
// viewers get an "agent_offline" status (the frontend shows a banner instead
// of a silently frozen screen) and the session stays active so the
// reconnect path can resume it.
func TestOnAgentOfflineMarksSession(t *testing.T) {
	r := New()
	r.Open("d1", "s1", 10)
	viewer, cancel := r.Subscribe("d1")
	defer cancel()

	// Drain the subscribe hello (sent before the offline mark).
	select {
	case hello, ok := <-viewer.Chan():
		if !ok || hello.Kind != "hello" {
			t.Fatalf("expected hello on subscribe, got %+v", hello)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no hello on subscribe")
	}

	r.OnAgentOffline("d1")

	if st := r.Status("d1"); st != StatusAgentOffline {
		t.Fatalf("status = %q, want %q", st, StatusAgentOffline)
	}
	select {
	case evt, ok := <-viewer.Chan():
		if !ok {
			t.Fatal("viewer channel closed on offline mark")
		}
		if evt.Kind != "status" || evt.Status != StatusAgentOffline {
			t.Fatalf("viewer did not get the offline status: %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no offline status pushed to viewer")
	}
	// Session still active (resumable on reconnect).
	if id, ok := r.ActiveSession("d1"); !ok || id != "s1" {
		t.Fatalf("session must stay active while the agent is offline: %q %v", id, ok)
	}
}

// TestPullCapEnforced verifies a file pull is bounded: once the byte cap is
// hit, the transfer is marked done+error and the buffer released (unbounded
// server memory was the old behavior).
func TestPullCapEnforced(t *testing.T) {
	const cap = 32
	r := New()
	r.maxPullBytes = cap
	r.Open("d1", "s1", 10)
	r.BeginPull("d1", "pull1", "/etc/passwd")

	// 20 + 20 = 40 bytes > 32 cap.
	r.OnChunk("d1", &agentv1.FileChunk{CommandId: "pull1", Data: make([]byte, 20), TotalBytes: 100})
	r.OnChunk("d1", &agentv1.FileChunk{CommandId: "pull1", Data: make([]byte, 20), TotalBytes: 100})

	tr, ok := r.PullState("pull1")
	if !ok {
		t.Fatal("transfer missing")
	}
	if !tr.Done {
		t.Fatal("transfer not marked done at cap")
	}
	if tr.Err == nil {
		t.Fatal("no error recorded at cap")
	}
	if len(tr.Data) != 0 {
		t.Fatalf("buffer not released at cap: %d bytes", len(tr.Data))
	}
	// Further chunks for a done transfer are dropped.
	r.OnChunk("d1", &agentv1.FileChunk{CommandId: "pull1", Data: make([]byte, 8), Eof: true})
	tr, _ = r.PullState("pull1")
	if len(tr.Data) != 0 {
		t.Fatal("chunk accepted after done")
	}
}

// TestPullDeviceMismatch: chunks for a device different from the transfer's
// device are dropped (a rogue stream cannot feed another device's transfer).
func TestPullDeviceMismatch(t *testing.T) {
	r := New()
	r.Open("d1", "s1", 10)
	r.BeginPull("d1", "pull1", "/etc/passwd")
	r.OnChunk("d1", &agentv1.FileChunk{CommandId: "pull1", Data: []byte("abc"), Eof: true})
	r.OnChunk("d2", &agentv1.FileChunk{CommandId: "pull1", Data: []byte("DEF")})

	tr, _ := r.PullState("pull1")
	if string(tr.Data) != "abc" {
		t.Fatalf("cross-device chunk accepted: %q", tr.Data)
	}
}

// TestSweepReapsCompletedTransfers: a finished file pull is reaped by Sweep
// after the done TTL (every pull's buffer used to sit in RAM for the
// process lifetime).
func TestSweepReapsCompletedTransfers(t *testing.T) {
	r := New()
	r.doneTTL = time.Millisecond
	r.Open("d1", "s1", 10)
	r.BeginPull("d1", "pull1", "/etc/passwd")
	r.OnChunk("d1", &agentv1.FileChunk{CommandId: "pull1", Data: []byte("payload"), Eof: true})

	if _, ok := r.PullState("pull1"); !ok {
		t.Fatal("completed transfer missing before sweep")
	}

	time.Sleep(5 * time.Millisecond)
	r.Sweep(time.Now())

	if _, ok := r.PullState("pull1"); ok {
		t.Fatal("completed transfer not reaped after doneTTL")
	}
}

// TestSweepLeavesActiveThings: Sweep must not touch a live session, a live
// viewer, or an in-flight (unfinished) transfer, even past the idle TTL.
func TestSweepLeavesActiveThings(t *testing.T) {
	r := New()
	r.idleTTL = time.Millisecond
	r.doneTTL = time.Millisecond // only affects DONE transfers
	r.Open("d1", "s1", 10)
	viewer, cancel := r.Subscribe("d1")
	defer cancel()
	_ = viewer
	// In-flight transfer (not done) — kept until the 2h inflight TTL.
	r.BeginPull("d1", "pull2", "/big/file")
	r.OnChunk("d1", &agentv1.FileChunk{CommandId: "pull2", Data: []byte("partial")})

	time.Sleep(5 * time.Millisecond)
	r.Sweep(time.Now())

	if id, ok := r.ActiveSession("d1"); !ok || id != "s1" {
		t.Fatalf("live session reaped by sweep: %q %v", id, ok)
	}
	if _, ok := r.PullState("pull2"); !ok {
		t.Fatal("in-flight transfer reaped by sweep")
	}
}

// TestCloseClearsStateAndReopen: stopping a session resets the device state
// (a fresh start begins with a clean slate), and a re-open with a new id
// works.
func TestCloseClearsStateAndReopen(t *testing.T) {
	r := New()
	r.Open("d1", "s1", 10)
	r.OnFrame("d1", frame("s1", []byte("f1")))
	r.Close("d1", "s1")

	if f, ok := r.LatestFrame("d1"); ok {
		t.Fatalf("latest frame not cleared on close: %q", f.JPEGB64)
	}
	if st := r.Status("d1"); st != "" {
		t.Fatalf("status not cleared on close: %q", st)
	}

	r.Open("d1", "s2", 10)
	if id, ok := r.ActiveSession("d1"); !ok || id != "s2" {
		t.Fatalf("re-open failed: %q %v", id, ok)
	}
}
