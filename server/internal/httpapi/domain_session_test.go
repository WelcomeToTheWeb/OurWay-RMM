package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/welcometotheweb/ourway-rmm/proto/gen/ourway-rmm/agent/v1"
	"github.com/welcometotheweb/ourway-rmm/server/internal/sessionrelay"
	"github.com/welcometotheweb/ourway-rmm/server/internal/store"
	"github.com/welcometotheweb/ourway-rmm/server/internal/users"
)

// sessionAuditRec is one recorded audit hop (test recorder).
type sessionAuditRec struct {
	deviceID string
	event    string
	data     map[string]any
}

// recordingSessionSink records downlinks + audit hops.
type recordingSessionSink struct {
	mu        sync.Mutex
	downlinks []recordedControl
	audits    []sessionAuditRec
	online    bool
}

type recordedControl struct {
	deviceID string
	sc       *agentv1.SessionControl
}

func (r *recordingSessionSink) push(deviceID string, sc *agentv1.SessionControl) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.downlinks = append(r.downlinks, recordedControl{deviceID: deviceID, sc: sc})
}

func (r *recordingSessionSink) audit(deviceID, event string, data map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audits = append(r.audits, sessionAuditRec{deviceID: deviceID, event: event, data: data})
}

func (r *recordingSessionSink) auditEvents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.audits))
	for _, a := range r.audits {
		out = append(out, a.event)
	}
	return out
}

func (r *recordingSessionSink) setOpen(online bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.online = online
}

// newSessionTestServer builds a Server with the session relay wired, a
// recording downlink and a recording audit sink.
func newSessionTestServer(t *testing.T) (*Server, *sessionrelay.Registry, *store.MemoryDeviceStore, *store.MemoryUserStore, *recordingSessionSink) {
	t.Helper()
	devs := store.NewMemoryDeviceStore()
	if err := devs.Register(context.Background(),
		"dev-abc", "fileserver-01", "linux", "amd64", "0.1.0", []string{"10.0.0.9"}, 30, 30); err != nil {
		t.Fatalf("register: %v", err)
	}
	usr := store.NewMemoryUserStore()
	sink := &recordingSessionSink{online: true}
	rateLimit := false
	s := New(Config{
		Devices:        devs,
		Clients:        store.NewMemoryClientStore(),
		Users:          usr,
		JWTSecret:      []byte("test-secret"),
		TokenLifetime:  time.Hour,
		AdminUser:      "admin",
		AdminPassword:  "s3cret",
		LoginRateLimit: &rateLimit,
		Sessions:       sessionrelay.New(),
		SendSessionControl: func(deviceID string, sc *agentv1.SessionControl) bool {
			sink.push(deviceID, sc)
			sink.mu.Lock()
			online := sink.online
			sink.mu.Unlock()
			return online
		},
		SessionAudit: func(ctx context.Context, deviceID, event string, data map[string]any) {
			sink.audit(deviceID, event, data)
		},
	})
	return s, s.sessions, devs, usr, sink
}

// adminSessionToken mints an admin session JWT (same shape login mints).
func adminSessionToken(t *testing.T) string {
	t.Helper()
	tok, err := users.MintSessionJWT([]byte("test-secret"), time.Hour, users.SessionClaims{Role: "admin", Username: "admin"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return tok
}

// startSessionPOST starts a session and returns the parsed response.
func startSessionPOST(t *testing.T, s *Server, token, deviceID string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	body, _ := json.Marshal(map[string]any{"fps": 2})
	req := httptest.NewRequest(http.MethodPost, "/api/devices/"+deviceID+"/session/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return rec.Code, out
}

// waitForBody polls the recorder body until it contains substr (SSE handlers
// block, so the body grows as the handler goroutine writes).
func waitForBody(t *testing.T, rec *httptest.ResponseRecorder, substr string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b := rec.Body.String(); strings.Contains(b, substr) {
			return b
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in SSE body; body so far: %s", substr, rec.Body.String())
	return ""
}

// TestSessionStreamRequiresTicket: the SSE endpoint must reject requests
// without a valid, unexpired, device-bound ticket (the operator JWT in the
// URL was the old auth — it leaked into access logs).
func TestSessionStreamRequiresTicket(t *testing.T) {
	s, _, _, _, _ := newSessionTestServer(t)
	tok := adminSessionToken(t)

	code, body := startSessionPOST(t, s, tok, "dev-abc")
	if code != http.StatusOK {
		t.Fatalf("start: got %d %v", code, body)
	}
	ticket, _ := body["stream_ticket"].(string)
	if ticket == "" {
		t.Fatalf("no stream_ticket in start response: %v", body)
	}

	getStream := func(query string) int {
		mux := http.NewServeMux()
		s.Register(mux)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-abc/session/stream?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		// Invalid-ticket paths 403 immediately; a valid ticket streams
		// (handler blocks) — distinguish by whether it returns in time.
		done := make(chan int, 1)
		go func() {
			mux.ServeHTTP(rec, req)
			done <- rec.Result().StatusCode
		}()
		select {
		case code := <-done:
			return code
		case <-time.After(500 * time.Millisecond):
			cancel()
			<-done
			return http.StatusOK
		}
	}

	// No ticket -> 403.
	if code := getStream(""); code != http.StatusForbidden {
		t.Fatalf("no ticket: got %d, want 403", code)
	}
	// Garbage ticket -> 403.
	if code := getStream("ticket=st-bogus"); code != http.StatusForbidden {
		t.Fatalf("garbage ticket: got %d, want 403", code)
	}
	// Valid ticket -> streams (200).
	if code := getStream("ticket=" + ticket); code != http.StatusOK {
		t.Fatalf("valid ticket: got %d, want 200", code)
	}
}

// TestSessionStreamDeliversNamedSSEEvents is the end-to-end proof for the
// critical fix: the server emits NAMED events (`event: frame`), and a frame
// pushed via the relay actually arrives on the stream with the snake_case
// JSON fields the frontend reads (kind, jpeg_b64, width, height, seq).
// Before this the frontend used onmessage (never fires for named events)
// AND read fields the server never emitted — so nothing rendered.
func TestSessionStreamDeliversNamedSSEEvents(t *testing.T) {
	s, reg, _, _, _ := newSessionTestServer(t)
	tok := adminSessionToken(t)

	code, body := startSessionPOST(t, s, tok, "dev-abc")
	if code != http.StatusOK {
		t.Fatalf("start: got %d %v", code, body)
	}
	ticket, _ := body["stream_ticket"].(string)
	sessionID, _ := body["session_id"].(string)
	if ticket == "" || sessionID == "" {
		t.Fatalf("missing ticket/session: %v", body)
	}

	mux := http.NewServeMux()
	s.Register(mux)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-abc/session/stream?ticket="+ticket, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	go mux.ServeHTTP(rec, req)

	// The stream established (hello event with the session id).
	waitForBody(t, rec, "event: hello", 2*time.Second)

	// Push a captured frame through the relay (as ingest would).
	reg.OnFrame("dev-abc", &agentv1.SessionFrame{
		SessionId:   sessionID,
		Seq:         7,
		Codec:       "jpeg",
		Width:       640,
		Height:      480,
		Jpeg:        []byte("JPEGDATA"),
		CaptureTsMs: time.Now().UnixMilli(),
	})

	// The frame arrives as a NAMED event.
	b := waitForBody(t, rec, "event: frame", 2*time.Second)
	if !strings.Contains(b, `"kind":"frame"`) ||
		!strings.Contains(b, `"codec":"jpeg"`) ||
		!strings.Contains(b, `"width":640`) ||
		!strings.Contains(b, `"height":480`) ||
		!strings.Contains(b, `"seq":7`) {
		t.Fatalf("frame JSON missing snake_case fields the frontend reads: %s", b)
	}
	// The JPEG payload is base64-encoded in jpeg_b64.
	if !strings.Contains(b, `"jpeg_b64":"`) {
		t.Fatalf("jpeg_b64 missing: %s", b)
	}
}

// TestSessionStartStopDownlinkAndAudit: start pushes an open control
// (session id + fps) down the downlink and records a session.started audit;
// stop pushes a close control and records session.stopped.
func TestSessionStartStopDownlinkAndAudit(t *testing.T) {
	s, _, _, _, sink := newSessionTestServer(t)
	tok := adminSessionToken(t)

	code, body := startSessionPOST(t, s, tok, "dev-abc")
	if code != http.StatusOK {
		t.Fatalf("start: got %d %v", code, body)
	}
	sessionID, _ := body["session_id"].(string)

	// The open control must have gone to the agent with the session id.
	sink.mu.Lock()
	var opened bool
	for _, d := range sink.downlinks {
		if d.deviceID == "dev-abc" {
			if o := d.sc.GetOpen(); o != nil && o.GetSessionId() == sessionID && o.GetFps() == 2 {
				opened = true
			}
		}
	}
	sink.mu.Unlock()
	if !opened {
		t.Fatalf("open control not downlinked with session id: %+v", sink.downlinks)
	}

	// session.started audit recorded.
	if evs := sink.auditEvents(); !contains(evs, "session.started") {
		t.Fatalf("session.started not audited: %v", evs)
	}

	// Stop: must downlink a close for the same session id.
	mux := http.NewServeMux()
	s.Register(mux)
	stopBody, _ := json.Marshal(map[string]string{"session_id": sessionID})
	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-abc/session/stop", bytes.NewReader(stopBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stop: got %d", rec.Code)
	}
	sink.mu.Lock()
	var closed bool
	for _, d := range sink.downlinks {
		if c := d.sc.GetClose(); c != nil && c.GetSessionId() == sessionID {
			closed = true
		}
	}
	sink.mu.Unlock()
	if !closed {
		t.Fatalf("close control not downlinked: %+v", sink.downlinks)
	}
	if evs := sink.auditEvents(); !contains(evs, "session.stopped") {
		t.Fatalf("session.stopped not audited: %v", evs)
	}
}

// TestSessionStopMismatchedID: stopping a session id that isn't the active
// one must 404 (it used to report success without doing anything).
func TestSessionStopMismatchedID(t *testing.T) {
	s, _, _, _, sink := newSessionTestServer(t)
	tok := adminSessionToken(t)

	code, body := startSessionPOST(t, s, tok, "dev-abc")
	if code != http.StatusOK {
		t.Fatalf("start: got %d %v", code, body)
	}
	_ = body

	mux := http.NewServeMux()
	s.Register(mux)
	stopBody, _ := json.Marshal(map[string]string{"session_id": "sess-not-the-active-one"})
	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-abc/session/stop", bytes.NewReader(stopBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("mismatched stop: got %d, want 404", rec.Code)
	}
	// No close control should have been downlinked for the bogus id.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, d := range sink.downlinks {
		if c := d.sc.GetClose(); c != nil && c.GetSessionId() == "sess-not-the-active-one" {
			t.Fatal("close downlinked for a non-active session id")
		}
	}
}

// TestSessionTenantScoping: a non-admin (tech) session scoped to one client
// can open a session on that client's device, but NOT on another client's
// device (the session routes used to skip the client check entirely).
func TestSessionTenantScoping(t *testing.T) {
	s, _, devs, usr, _ := newSessionTestServer(t)

	// A second client + one device in it.
	cs := store.NewMemoryClientStore()
	acme, err := cs.Create(context.Background(), "Acme Corp", "")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := devs.Register(context.Background(),
		"dev-acme", "acme-web-01", "linux", "amd64", "0.1.0", []string{"10.0.0.20"}, 30, 30); err != nil {
		t.Fatalf("register acme dev: %v", err)
	}
	if err := devs.SetClient(context.Background(), "dev-acme", acme.ID); err != nil {
		t.Fatalf("set client: %v", err)
	}
	// dev-abc stays unassigned (a different client from acme).

	// A tech scoped to acme only.
	if _, err := usr.Create(context.Background(), "tech1", "tech", "pw-12345", []string{acme.ID}); err != nil {
		t.Fatalf("create tech: %v", err)
	}
	techTok, err := users.MintSessionJWT([]byte("test-secret"), time.Hour, users.SessionClaims{Role: "tech", Username: "tech1"})
	if err != nil {
		t.Fatalf("mint tech: %v", err)
	}

	// Allowed: the tech's own client's device.
	code, _ := startSessionPOST(t, s, techTok, "dev-acme")
	if code != http.StatusOK {
		t.Fatalf("scoped device: got %d, want 200", code)
	}
	// Denied: the unassigned (other client) device.
	code, _ = startSessionPOST(t, s, techTok, "dev-abc")
	if code != http.StatusForbidden {
		t.Fatalf("other client's device: got %d, want 403", code)
	}
}

// TestSessionStartAgentOffline: when the downlink is down the start must 502
// (and not leave a dangling registered session).
func TestSessionStartAgentOffline(t *testing.T) {
	s, reg, _, _, sink := newSessionTestServer(t)
	tok := adminSessionToken(t)
	sink.setOpen(false)

	code, _ := startSessionPOST(t, s, tok, "dev-abc")
	if code != http.StatusBadGateway {
		t.Fatalf("offline start: got %d, want 502", code)
	}
	if _, ok := reg.ActiveSession("dev-abc"); ok {
		t.Fatal("session left registered after a failed start")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
