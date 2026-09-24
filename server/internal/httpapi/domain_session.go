// Package httpapi: domain_session.go implements gap #1a's remote session
// operator API: start/stop a session on a device, stream frames to a
// browser viewer via SSE, and download files pulled via file_pull.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	agentv1 "github.com/welcometotheweb/ourway-rmm/proto/gen/ourway-rmm/agent/v1"
	"github.com/welcometotheweb/ourway-rmm/server/internal/store"
	"github.com/welcometotheweb/ourway-rmm/server/internal/users"
	"github.com/welcometotheweb/ourway-rmm/server/internal/sessionrelay"
)

// streamTicketTTL bounds how long a minted stream ticket is accepted by the
// SSE endpoint (the browser connects right after session start).
const streamTicketTTL = 5 * time.Minute

// streamTicket is a short-lived, device+user-bound credential for the SSE
// stream endpoint. EventSource cannot set auth headers, so without a ticket
// the full operator JWT would ride in the URL query string — and therefore
// into server access logs, reverse-proxy logs and browser network tooling.
// A leaked ticket expires within minutes and only opens the screen stream
// for the device it was minted for, never any other operator capability.
type streamTicket struct {
	deviceID  string
	sessionID string
	user      string
	expires   time.Time
}

// requireDeviceAccess (gap #3) resolves the device (404 if unknown) and
// scopes non-admin sessions to the device's client, mirroring the device
// sub-route dispatch. The session routes used to skip this entirely — any
// authenticated viewer could stream any device in the estate.
func (s *Server) requireDeviceAccess(w http.ResponseWriter, r *http.Request, deviceID string) bool {
	dev, err := s.devices.Get(r.Context(), deviceID)
	if err != nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return false
	}
	cid := dev.ClientID
	if cid == "" {
		cid = store.DefaultClientID
	}
	return requireClientAccess(w, r, cid)
}

// auditSession emits a session audit hop onto the event bus (journaled by
// the webhook framework, visible on /events). Nil-safe (in-memory mode).
func (s *Server) auditSession(ctx context.Context, deviceID, event string, data map[string]any) {
	if s.sessionAudit != nil {
		s.sessionAudit(ctx, deviceID, event, data)
	}
}

// newSessionID mints a session id from a CSPRNG. (The old format was
// `sess-<nanotime>-<nanotime>>32` — fully derivable from the wall clock.)
func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is effectively impossible; fall back to a
		// time-based id rather than failing the session start.
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return "sess-" + hex.EncodeToString(b)
}

// mintStreamTicket issues a stream ticket bound to one device session and
// one operator (see streamTicket). Expired tickets are swept lazily.
func (s *Server) mintStreamTicket(deviceID, sessionID, user string) string {
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	if s.streamTickets == nil {
		s.streamTickets = make(map[string]streamTicket)
	}
	now := time.Now()
	for k, t := range s.streamTickets {
		if now.After(t.expires) {
			delete(s.streamTickets, k)
		}
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := "st-" + hex.EncodeToString(b)
	s.streamTickets[tok] = streamTicket{deviceID: deviceID, sessionID: sessionID, user: user, expires: now.Add(streamTicketTTL)}
	return tok
}

// validStreamTicket reports whether ticket is unexpired and bound to this
// device + operator.
func (s *Server) validStreamTicket(ticket, deviceID, user string) (sessionID string, ok bool) {
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	t, exists := s.streamTickets[ticket]
	if !exists || time.Now().After(t.expires) || t.deviceID != deviceID {
		return "", false
	}
	if user != "" && t.user != "" && t.user != user {
		return "", false
	}
	return t.sessionID, true
}

// handleSessionStart opens a remote session on a device.
//
//	POST /api/devices/{id}/session/start {"fps":2}
//
//	200 {session_id, fps, device_id}
//	400 bad body / unknown fps range
//	403 session lacks role
//	404 unknown device
//	502 device has no live stream (offline)
//	503 session relay not wired (in-memory mode)
func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request, deviceID string) {
	if s.sessions == nil {
		http.Error(w, "session relay not configured", http.StatusServiceUnavailable)
		return
	}
	if s.sendSessionControl == nil {
		http.Error(w, "session control not configured", http.StatusServiceUnavailable)
		return
	}
	if !s.requireDeviceAccess(w, r, deviceID) {
		return
	}

	// Parse optional fps (default 0 = agent default). Cap the body: a session
	// start is a small JSON object, not an upload.
	var body struct {
		FPS *int `json:"fps"`
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
	}
	fps := 0
	if body.FPS != nil {
		fps = *body.FPS
		if fps < 0 || fps > 30 {
			http.Error(w, "fps must be 0-30 (0 = default)", http.StatusBadRequest)
			return
		}
	}

	// Generate a session ID (CSPRNG — see newSessionID).
	sessionID := newSessionID()

	// Register in the session relay so viewers can subscribe.
	s.sessions.Open(deviceID, sessionID, fps)

	// Send the open control downlink to the agent.
	sc := &agentv1.SessionControl{
		Action: &agentv1.SessionControl_Open{
			Open: &agentv1.SessionControl_OpenSession{
				SessionId: sessionID,
				Fps:       int32(fps),
			},
		},
	}
	if !s.sendSessionControl(deviceID, sc) {
		s.sessions.Close(deviceID, sessionID)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "device is offline (no live stream)",
		})
		return
	}

	// Mint the short-lived stream ticket the browser viewer presents to the
	// SSE endpoint (so the operator JWT never appears in the stream URL).
	sess, _ := users.SessionFromContext(r.Context())
	user := sess.Username
	ticket := s.mintStreamTicket(deviceID, sessionID, user)

	// Audit: who started a session on which device (the compliance answer
	// to "who had remote access").
	s.auditSession(r.Context(), deviceID, "session.started", map[string]any{
		"user":       user,
		"session_id": sessionID,
		"fps":        fps,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":    sessionID,
		"fps":           fps,
		"device_id":     deviceID,
		"stream_ticket": ticket,
	})
}

// handleSessionStop closes a remote session on a device.
//
//	POST /api/devices/{id}/session/stop {"session_id":"sess-..."}
//
//	200 {stopped: true}
//	404 unknown device / no active session
//	502 device has no live stream
//	503 session relay not wired
func (s *Server) handleSessionStop(w http.ResponseWriter, r *http.Request, deviceID string) {
	if s.sessions == nil {
		http.Error(w, "session relay not configured", http.StatusServiceUnavailable)
		return
	}
	if !s.requireDeviceAccess(w, r, deviceID) {
		return
	}

	var body struct {
		SessionID string `json:"session_id"`
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
	}

	// Look up the active session ID from the relay. A caller-supplied id
	// must MATCH the active session — stopping an arbitrary (possibly
	// superseded) id used to report success without doing anything.
	activeID, hasActive := s.sessions.ActiveSession(deviceID)
	if !hasActive && body.SessionID == "" {
		http.Error(w, "no active session for device", http.StatusNotFound)
		return
	}
	sessionID := body.SessionID
	if sessionID == "" {
		sessionID = activeID
	} else if sessionID != activeID {
		http.Error(w, "session id does not match the active session", http.StatusNotFound)
		return
	}

	// Duration for the audit hop (best effort — openedAt is set on Open).
	var durationMS int64
	if _, _, openedAt, ok := s.sessions.SessionInfo(deviceID); ok && !openedAt.IsZero() {
		durationMS = time.Since(openedAt).Milliseconds()
	}

	// Send the close control downlink.
	sc := &agentv1.SessionControl{
		Action: &agentv1.SessionControl_Close{
			Close: &agentv1.SessionControl_CloseSession{
				SessionId: sessionID,
			},
		},
	}
	stopped := true
	if s.sendSessionControl != nil && !s.sendSessionControl(deviceID, sc) {
		// Agent is offline; mark session as closed locally.
		stopped = false
	}
	s.sessions.Close(deviceID, sessionID)

	sess, _ := users.SessionFromContext(r.Context())
	s.auditSession(r.Context(), deviceID, "session.stopped", map[string]any{
		"user":         sess.Username,
		"session_id":   sessionID,
		"duration_ms":  durationMS,
		"agent_online": stopped,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"stopped":    stopped,
		"session_id": sessionID,
		"device_id":  deviceID,
	})
}

// handleSessionStream streams session frames to a browser viewer via SSE.
//
//	GET /api/devices/{id}/session/stream
//
//	200 text/event-stream (frames until disconnected)
//	404 unknown device
//	503 session relay not wired
func (s *Server) handleSessionStream(w http.ResponseWriter, r *http.Request, deviceID string) {
	if s.sessions == nil {
		http.Error(w, "session relay not configured", http.StatusServiceUnavailable)
		return
	}
	if !s.requireDeviceAccess(w, r, deviceID) {
		return
	}

	// The stream endpoint is authenticated by a short-lived stream ticket
	// (minted at session start, bound to device + operator) — NOT by the
	// operator JWT, which EventSource can only pass via the URL query
	// string (and which would then leak into access logs).
	sess, _ := users.SessionFromContext(r.Context())
	user := sess.Username
	ticket := r.URL.Query().Get("ticket")
	if _, ok := s.validStreamTicket(ticket, deviceID, user); !ok {
		http.Error(w, "invalid or expired stream ticket (start a session first)", http.StatusForbidden)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Write([]byte(": connected\n\n"))
	flusher.Flush()

	viewer, cancel := s.sessions.Subscribe(deviceID)
	defer cancel() // last-viewer disconnect closes the session server-side

	ctx := r.Context()
	s.auditSession(ctx, deviceID, "session.stream_opened", map[string]any{
		"user": user,
		"session_id": func() string {
			if id, ok := s.sessions.ActiveSession(deviceID); ok {
				return id
			}
			return ""
		}(),
	})
	startedAt := time.Now()
	defer func() {
		// The bus wiring publishes on a background context, so this audit
		// hop survives the request context cancellation.
		s.auditSession(ctx, deviceID, "session.stream_closed", map[string]any{
			"user":        user,
			"duration_ms": time.Since(startedAt).Milliseconds(),
		})
	}()

	// Send initial status if available.
	if status := s.sessions.Status(deviceID); status != "" {
		evt := sessionrelay.FrameEvent{Kind: "status", Status: status}
		s.writeSSEEvent(w, flusher, "status", evt)
	}

	// Keepalive so proxies and the browser don't treat a stalled agent as a
	// dead connection (and the viewer can distinguish "paused" from "dead").
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-viewer.Done():
			return
		case <-keepalive.C:
			w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case evt, ok := <-viewer.Chan():
			if !ok {
				return
			}
			s.writeSSEEvent(w, flusher, evt.Kind, evt)
		}
	}
}

// writeSSEEvent writes one SSE event frame.
func (s *Server) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, kind string, evt sessionrelay.FrameEvent) {
	data, _ := json.Marshal(evt)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, string(data))
	flusher.Flush()
}

// handleFileDownload serves a file that was pulled via file_pull.
//
//	GET /api/devices/{id}/session/files/{command_id}
//
//	200 file bytes (with Content-Disposition)
//	202 transfer in progress
//	404 unknown command / transfer not found
//	503 session relay not wired
func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request, deviceID, commandID string) {
	if s.sessions == nil {
		http.Error(w, "session relay not configured", http.StatusServiceUnavailable)
		return
	}
	if !s.requireDeviceAccess(w, r, deviceID) {
		return
	}

	t, ok := s.sessions.PullState(commandID)
	if !ok {
		http.Error(w, "transfer not found", http.StatusNotFound)
		return
	}
	// A transfer belongs to the device that pulled it — never serve one
	// device's file under another device's route.
	if t.DeviceID != "" && t.DeviceID != deviceID {
		http.Error(w, "transfer not found", http.StatusNotFound)
		return
	}

	if !t.Done {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"state":    "in_progress",
			"received": t.Received,
			"total":    t.Total,
		})
		return
	}

	if t.Err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": t.Err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(t.Path)))
	w.Header().Set("Content-Length", strconv.FormatInt(t.Received, 10))
	w.Write(t.Data)
}

// registerSession registers the remote session routes.
func registerSession(s *Server, mux *http.ServeMux) {
	// POST /api/devices/{id}/session/start (admin/tech)
	mux.HandleFunc("/api/devices/{id}/session/start", s.rbacRoleGate(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			s.handleSessionStart(w, r, id)
		}, "admin", "tech"))

	// POST /api/devices/{id}/session/stop (admin/tech)
	mux.HandleFunc("/api/devices/{id}/session/stop", s.rbacRoleGate(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			s.handleSessionStop(w, r, id)
		}, "admin", "tech"))

	// GET /api/devices/{id}/session/stream (any authenticated operator)
	mux.HandleFunc("/api/devices/{id}/session/stream", s.rbacGate(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			s.handleSessionStream(w, r, id)
		}))

	// GET /api/devices/{id}/session/files/{command_id} (admin/tech)
	mux.HandleFunc("/api/devices/{id}/session/files/{command_id}", s.rbacRoleGate(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			cmd := r.PathValue("command_id")
			s.handleFileDownload(w, r, id, cmd)
		}, "admin", "tech"))

	// Mirror under /admin prefix.
	mux.HandleFunc("/admin/devices/{id}/session/start", s.rbacRoleGate(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			s.handleSessionStart(w, r, id)
		}, "admin", "tech"))

	mux.HandleFunc("/admin/devices/{id}/session/stop", s.rbacRoleGate(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			s.handleSessionStop(w, r, id)
		}, "admin", "tech"))

	mux.HandleFunc("/admin/devices/{id}/session/stream", s.rbacGate(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			s.handleSessionStream(w, r, id)
		}))

	mux.HandleFunc("/admin/devices/{id}/session/files/{command_id}", s.rbacRoleGate(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			cmd := r.PathValue("command_id")
			s.handleFileDownload(w, r, id, cmd)
		}, "admin", "tech"))
}
