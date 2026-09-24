// Package sessionrelay is the server side of gap #1a (remote session). It
// receives SessionFrame uplink frames from ingest, keeps only the latest
// frame per device (drop-old — a slow viewer never backs the agent up),
// and streams frames to subscribed browser viewers via SSE. It also
// accumulates FileChunk frames for file_pull transfers.
//
// Design: one Registry instance shared between ingest (OnFrame/OnChunk)
// and the HTTP API (Subscribe/TransferState). No persistence (phase 1).
package sessionrelay

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	agentv1 "github.com/welcometotheweb/ourway-rmm/proto/gen/ourway-rmm/agent/v1"
)

// Pull bounds + GC TTLs (a single pull must not be able to OOM the server,
// and finished work must not stay resident for the process lifetime).
const (
	// DefaultMaxPullBytes caps one file_pull transfer (1 GiB).
	DefaultMaxPullBytes = int64(1 << 30)
	// doneTransferTTL keeps completed transfers downloadable this long.
	doneTransferTTL = time.Hour
	// inflightTransferTTL bounds a stuck (no-eof) transfer.
	inflightTransferTTL = 2 * time.Hour
	// deviceIdleTTL drops device state that has no session, no viewers and
	// no recent frames (sweeper only; never touches a live session).
	deviceIdleTTL = 10 * time.Minute
)

// StatusAgentOffline is reported (server-side) when the device's uplink
// drops mid-session: the viewer sees the degraded state instead of a
// silently frozen screen.
const StatusAgentOffline = "agent_offline"

// TransferState tracks an in-progress file_pull.
type TransferState struct {
	DeviceID  string
	Path      string
	Total     int64
	Received  int64
	Mode      uint32
	Data      []byte
	Done      bool
	Err       error
	StartedAt time.Time
	Complete  time.Time
}

// FrameEvent is one SSE frame sent to a viewer. JSON tags are the SSE wire
// contract (snake_case, matching the rest of the API) — the frontend reads
// data.kind / data.jpeg_b64 / data.status off these.
type FrameEvent struct {
	Kind      string `json:"kind"` // "frame" | "status" | "hello" | "goodbye"
	SessionID string `json:"session_id,omitempty"`
	Seq       uint64 `json:"seq,omitempty"`
	Codec     string `json:"codec,omitempty"`
	Width     uint32 `json:"width,omitempty"`
	Height    uint32 `json:"height,omitempty"`
	JPEGB64   string `json:"jpeg_b64,omitempty"` // base64 of the jpeg bytes
	CaptureTS int64  `json:"capture_ts_ms,omitempty"`
	Status    string `json:"status,omitempty"` // non-empty on status frames
}

// Viewer is a single subscribed browser viewer.
type Viewer struct {
	devID string
	ch    chan FrameEvent
	done  chan struct{}
}

// Chan returns the viewer's event channel.
func (v *Viewer) Chan() chan FrameEvent {
	return v.ch
}

// Done returns the viewer's done channel.
func (v *Viewer) Done() chan struct{} {
	return v.done
}

// DeviceState holds the live state for one device.
type DeviceState struct {
	sessionID   string
	fps         int
	latest      *FrameEvent
	status      string
	viewers     map[*Viewer]bool
	openedAt    time.Time // last Open (session audit: duration)
	lastFrameAt time.Time
}

// Registry is the central session relay. Thread-safe.
type Registry struct {
	mu        sync.Mutex
	devices   map[string]*DeviceState
	transfers map[string]*TransferState // command_id -> transfer
	// maxPullBytes bounds one file_pull transfer (0 = DefaultMaxPullBytes).
	maxPullBytes int64
	// GC TTLs, instance fields (defaults from the constants above) so tests
	// can shorten them. See Sweep.
	doneTTL     time.Duration
	inflightTTL time.Duration
	idleTTL     time.Duration
	// OnAutoClose (optional) fires when the LAST viewer disconnects while a
	// session is active: the server closes the session (sends the close
	// downlink via the httpapi/ingest wiring) so the agent stops capturing.
	// Called without the registry lock held.
	OnAutoClose func(devID, sessionID string)
}

// New creates a Registry with the default pull cap + GC TTLs.
func New() *Registry {
	return &Registry{
		devices:     make(map[string]*DeviceState),
		transfers:   make(map[string]*TransferState),
		doneTTL:     doneTransferTTL,
		inflightTTL: inflightTransferTTL,
		idleTTL:     deviceIdleTTL,
	}
}

// Open records that a session has been opened for a device (the server
// just sent SessionControl open). Viewers can subscribe before frames
// arrive; they'll get a hello event with the session info. A new open
// supersedes any prior state for the device (old session's latest frame
// and status are dropped — they belong to a different session).
func (r *Registry) Open(devID, sessionID string, fps int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.devices[devID]
	if ds == nil {
		ds = &DeviceState{viewers: make(map[*Viewer]bool)}
		r.devices[devID] = ds
	}
	ds.sessionID = sessionID
	ds.fps = fps
	ds.status = ""
	ds.latest = nil
	ds.openedAt = time.Now()
}

// OnFrame is called by ingest when a SessionFrame arrives from an agent.
// Status frames update the device's status; real frames are stored as
// the latest and sent to viewers (drop if their channel is full).
//
// Frames for a CLOSED or unknown session are dropped, and a frame must not
// revive a closed session by re-stamping the session id (the close
// downlink can race the agent's in-flight frames).
func (r *Registry) OnFrame(devID string, f *agentv1.SessionFrame) {
	r.mu.Lock()
	ds := r.devices[devID]
	if ds == nil || ds.sessionID == "" {
		r.mu.Unlock()
		return // no active session — never (re)create state from a frame
	}
	if sid := f.GetSessionId(); sid != "" && sid != ds.sessionID {
		r.mu.Unlock()
		return // stale frame from a superseded session
	}
	ds.lastFrameAt = time.Now()

	evt := FrameEvent{
		SessionID: f.GetSessionId(),
		Seq:       f.GetSeq(),
		Codec:     f.GetCodec(),
		Width:     f.GetWidth(),
		Height:    f.GetHeight(),
		CaptureTS: f.GetCaptureTsMs(),
		Status:    f.GetStatus(),
	}
	if f.GetStatus() != "" {
		evt.Kind = "status"
		ds.status = f.GetStatus()
	} else {
		evt.Kind = "frame"
		evt.JPEGB64 = base64.StdEncoding.EncodeToString(f.GetJpeg())
		ds.latest = &evt
	}
	// Copy viewer list (notify outside lock via non-blocking sends).
	viewers := make([]*Viewer, 0, len(ds.viewers))
	for v := range ds.viewers {
		viewers = append(viewers, v)
	}
	r.mu.Unlock()

	for _, v := range viewers {
		select {
		case v.ch <- evt:
		default:
			// Viewer is slow; drop this frame for it.
		}
	}
}

// Subscribe returns a channel of FrameEvents for a device. The viewer
// should call the returned cancel func when done. An initial "hello" event
// is sent with the current state, followed by the latest frame if one is
// available (so a (re)connecting viewer sees the screen immediately).
func (r *Registry) Subscribe(devID string) (*Viewer, func()) {
	r.mu.Lock()
	ds := r.devices[devID]
	if ds == nil {
		ds = &DeviceState{viewers: make(map[*Viewer]bool)}
		r.devices[devID] = ds
	}
	v := &Viewer{
		devID: devID,
		ch:    make(chan FrameEvent, 16),
		done:  make(chan struct{}),
	}
	ds.viewers[v] = true

	// Send hello event with session info, then the latest frame.
	hello := FrameEvent{
		Kind:      "hello",
		SessionID: ds.sessionID,
	}
	latest := ds.latest
	r.mu.Unlock()

	select {
	case v.ch <- hello:
	default:
	}
	if latest != nil {
		select {
		case v.ch <- *latest:
		default:
		}
	}

	cancelOnce := sync.Once{}
	cancel := func() {
		cancelOnce.Do(func() {
			r.mu.Lock()
			var autoCloseDev, autoCloseSess string
			if ds2 := r.devices[devID]; ds2 != nil {
				delete(ds2.viewers, v)
				if len(ds2.viewers) == 0 && ds2.sessionID != "" {
					// Last viewer left: close the session server-side. Without
					// this, a browser that dies (crash, closed tab, network
					// drop) leaves the agent capturing the screen forever.
					autoCloseDev, autoCloseSess = devID, ds2.sessionID
					ds2.sessionID = ""
					ds2.status = ""
					ds2.latest = nil
				}
				if ds2.sessionID == "" && len(ds2.viewers) == 0 && ds2.latest == nil && ds2.status == "" {
					delete(r.devices, devID)
				}
			}
			r.mu.Unlock()
			close(v.done)
			if autoCloseSess != "" && r.OnAutoClose != nil {
				r.OnAutoClose(autoCloseDev, autoCloseSess)
			}
		})
	}
	return v, cancel
}

// OnChunk is called by ingest when a FileChunk arrives for a file_pull.
// Chunks are accumulated (bounded by maxPullBytes); the eof chunk
// finalizes the transfer.
func (r *Registry) OnChunk(devID string, c *agentv1.FileChunk) {
	r.mu.Lock()
	t := r.transfers[c.GetCommandId()]
	if t == nil || t.Done {
		r.mu.Unlock()
		return // No transfer registered (or already complete)
	}
	if t.DeviceID != "" && t.DeviceID != devID {
		r.mu.Unlock()
		return // chunk for another device's transfer
	}
	if t.Err != nil {
		r.mu.Unlock()
		return
	}
	if c.GetTotalBytes() != 0 {
		t.Total = c.GetTotalBytes()
	}
	if c.GetSourceMode() != 0 {
		t.Mode = c.GetSourceMode()
	}
	limit := r.maxPullBytes
	if limit <= 0 {
		limit = DefaultMaxPullBytes
	}
	if int64(len(t.Data))+int64(len(c.GetData())) > limit {
		t.Err = fmt.Errorf("pull exceeds limit (%d bytes)", limit)
		t.Done = true
		t.Complete = time.Now()
		t.Data = nil // release the buffer; the download route reports t.Err
		r.mu.Unlock()
		return
	}
	t.Received += int64(len(c.GetData()))
	t.Data = append(t.Data, c.GetData()...)
	if c.GetEof() {
		t.Done = true
		t.Complete = time.Now()
	}
	r.mu.Unlock()
}

// BeginPull registers a new file_pull transfer.
func (r *Registry) BeginPull(deviceID, commandID, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transfers[commandID] = &TransferState{
		DeviceID:  deviceID,
		Path:      path,
		StartedAt: time.Now(),
	}
}

// PullState returns the current state of a file_pull transfer.
func (r *Registry) PullState(commandID string) (*TransferState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.transfers[commandID]
	if !ok {
		return nil, false
	}
	// Return a copy for the caller.
	c := *t
	return &c, true
}

// ActiveSession returns the current session ID for a device.
func (r *Registry) ActiveSession(devID string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.devices[devID]
	if ds == nil || ds.sessionID == "" {
		return "", false
	}
	return ds.sessionID, true
}

// SessionInfo returns the active session's id, fps and start time (for the
// agent-reconnect re-open and the stop audit's duration). ok=false when the
// device has no active session.
func (r *Registry) SessionInfo(devID string) (sessionID string, fps int, openedAt time.Time, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.devices[devID]
	if ds == nil || ds.sessionID == "" {
		return "", 0, time.Time{}, false
	}
	return ds.sessionID, ds.fps, ds.openedAt, true
}

// OnAgentOffline marks a device's active session as degraded (the uplink
// dropped mid-session). The session itself stays active — when the agent
// reconnects the server re-sends the open control and capture resumes —
// but viewers get a "agent_offline" status frame instead of a silently
// frozen screen.
func (r *Registry) OnAgentOffline(devID string) {
	r.mu.Lock()
	ds := r.devices[devID]
	if ds == nil || ds.sessionID == "" || ds.status == StatusAgentOffline {
		r.mu.Unlock()
		return
	}
	ds.status = StatusAgentOffline
	evt := FrameEvent{Kind: "status", SessionID: ds.sessionID, Status: StatusAgentOffline}
	viewers := make([]*Viewer, 0, len(ds.viewers))
	for v := range ds.viewers {
		viewers = append(viewers, v)
	}
	r.mu.Unlock()
	for _, v := range viewers {
		select {
		case v.ch <- evt:
		default:
		}
	}
}

// LatestFrame returns the most recent frame for a device.
func (r *Registry) LatestFrame(devID string) (*FrameEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.devices[devID]
	if ds == nil || ds.latest == nil {
		return nil, false
	}
	return ds.latest, true
}

// Status returns the last status for a device.
func (r *Registry) Status(devID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.devices[devID]
	if ds == nil {
		return ""
	}
	return ds.status
}

// Close marks a session as closed (the server sent a SessionControl close
// downlink or the viewer disconnected). Future frames for this session are
// dropped (see OnFrame); the viewers get a "goodbye" event. An empty
// device entry is removed so the maps cannot grow unboundedly.
func (r *Registry) Close(devID, sessionID string) {
	r.mu.Lock()
	if ds := r.devices[devID]; ds != nil && (sessionID == "" || ds.sessionID == sessionID) {
		ds.sessionID = ""
		ds.status = ""
		ds.latest = nil // stale frame must not survive a close
		for v := range ds.viewers {
			select {
			case v.ch <- FrameEvent{Kind: "goodbye", SessionID: sessionID}:
			default:
			}
		}
	}
	if ds := r.devices[devID]; ds != nil && ds.sessionID == "" && len(ds.viewers) == 0 && ds.latest == nil && ds.status == "" {
		delete(r.devices, devID)
	}
	r.mu.Unlock()
}

// Sweep drops stale state: empty device entries idle past deviceIdleTTL,
// completed transfers past doneTransferTTL, and stuck (no-eof) transfers
// past inflightTransferTTL.
func (r *Registry) Sweep(now time.Time) {
	r.mu.Lock()
	for devID, ds := range r.devices {
		empty := ds.sessionID == "" && len(ds.viewers) == 0
		idle := ds.lastFrameAt.IsZero() || now.Sub(ds.lastFrameAt) > r.idleTTL
		if empty && idle {
			delete(r.devices, devID)
		}
	}
	for id, t := range r.transfers {
		if t.Done && now.Sub(t.Complete) > r.doneTTL {
			delete(r.transfers, id)
		} else if !t.Done && now.Sub(t.StartedAt) > r.inflightTTL {
			delete(r.transfers, id)
		}
	}
	r.mu.Unlock()
}

// SweepLoop runs Sweep on a 30s ticker until ctx is done.
func (r *Registry) SweepLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Sweep(time.Now())
		}
	}
}

// FormatBytes formats a byte count for display.
func FormatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}
