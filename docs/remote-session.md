# Remote Session Protocol

The remote session feature provides live screen viewing of managed devices
through the OurWay RMM agent. Phase 1 is view-only; phase 2 adds two-way
mouse/keyboard control. This document describes the architecture, protocol,
and environment configuration.

## Architecture

Phase 1 is **view-only**: frames flow one way (agent → server → browser).
Two-way input is a phase 2 feature (see [Phase 2](#phase-2-input)).

```
                         gRPC uplink          gRPC downlink
 Agent Driver ── SessionFrame ──▶ Server Relay ◀── SessionControl ── (phase 2 input)
                              │
                         SSE named events
                              ▼
                         Browser Viewer
```

- **Uplink:** the agent captures JPEG frames and sends `SessionFrame` over its
  existing mTLS gRPC stream (the same stream as heartbeats/metrics).
- **Relay:** the server keeps only the **latest** frame per session (drop-old)
  and fans it out to every connected viewer.
- **Downlink:** the server drives the agent with `SessionControl` frames
  (`Open` / `Close`, and — in phase 2 — mouse/keyboard events).
- **Viewer transport:** Server-Sent Events (SSE). The browser presents a
  short-lived **stream ticket** (not the operator JWT) to open the stream.

### Components

1. **Session Driver** (`agent/internal/session/session.go`)
   - Runs the capture loop for one active session per device
   - Driven by `SessionControl` downlink frames from the server
   - Streams `SessionFrame` uplink frames at the configured frame rate

2. **Capture Backends** (`agent/internal/session/capture_*.go`)
   - Platform-specific screen capture implementations
   - Linux: pure-Go X11 core-client `GetImage` on the root window
     (capped at 1280x720, JPEG quality 80); reports `vnc_required` status
     frames when `DISPLAY` is unset
   - Windows: raw GDI `BitBlt` via syscalls (no cgo, no PowerShell)
   - macOS: not implemented - the stub reports `unavailable` status frames
     (a ScreenCaptureKit backend is planned for phase 2)
   - Test backend (`capture_test_backend.go`): synthetic animated frames

3. **Input Relayer** (`agent/internal/session/input_*.go`)
   - Receives mouse/keyboard events from the server (phase 2)
   - Injects events into the local desktop environment
   - **Phase 1 is view-only**, but the relayer is already safety-gated:
     - Events are dropped unless their `session_id` matches the currently
       live session (a stale/duplicated event for a closed session is a no-op)
     - Any key event while the **Meta/Win** key is held is blocked, so a
       remote viewer cannot trigger `Win+L` (lock), `Win+R` (run), `Win+X`
       (power) or the Start-menu/search sequences
   - Backends that cannot inject input return an explicit error (never a
     fake success), so the server can surface the real cause

## Protocol

### Frame Types (proto/ourway-rmm/agent/v1/agent.proto)

#### Uplink: SessionFrame

```protobuf
message SessionFrame {
  string session_id = 1;   // Echo of SessionControl.open session_id
  uint64 seq = 2;          // Monotonic frame counter (resets per open)
  string codec = 3;        // Always "jpeg" in phase 1
  uint32 width = 4;
  uint32 height = 5;
  bytes jpeg = 6;          // The frame (empty on status frames)
  int64 capture_ts_ms = 7; // Agent wall clock at capture (Unix ms)
  string status = 8;       // Non-empty = status frame (see below)
}
```

Status frame values:

- `"vnc_required"` — No local display to capture (e.g., headless Linux box)
- `"unavailable"` — Capture backend errored
- `"agent_offline"` — **Server-side**: the device's uplink dropped while a
  session is open. Viewers get a status frame instead of a silent freeze.
  The session is **not torn down** — when the agent reconnects, the server
  re-sends the `Open` control and the session resumes.

#### Downlink: SessionControl

```protobuf
message SessionControl {
  oneof action {
    OpenSession open = 1;
    CloseSession close = 2;
    MouseEvent mouse_event = 10;       // Phase 2
    KeyboardEvent keyboard_event = 11; // Phase 2
  }
}
```

##### OpenSession

```protobuf
message OpenSession {
  string session_id = 1;  // Server-minted session id
  int32 fps = 2;          // Frame rate hint (0 = agent default)
}
```

##### MouseEvent

```protobuf
message MouseEvent {
  string session_id = 1;
  string event_type = 2;  // "move" | "down" | "up" | "click" | "wheel"
  int32 x = 3;            // Absolute screen coordinates
  int32 y = 4;
  int32 button = 5;       // 0=left, 1=middle, 2=right (0 for move/wheel)
  int32 wheel_delta = 6;  // Positive=up, negative=down
}
```

##### KeyboardEvent

```protobuf
message KeyboardEvent {
  string session_id = 1;
  string event_type = 2;  // "down" | "up" | "key"
  uint32 codepoint = 3;   // Unicode codepoint (0 for modifier-only)
  uint32 modifiers = 4;   // Bitmask: shift=1, ctrl=2, alt=4, meta=8
}
```

### Session Lifecycle

The lifecycle is driven by three HTTP routes plus the viewer's SSE
subscription. All four session routes are **tenant-scoped**: an operator may
only act on a device belonging to a client they can see (admins/legacy see
all clients; tech/viewer sessions are limited to their granted clients).

1. **Start** — `POST /api/devices/{id}/session/start`
   - The server mints a **cryptographically random** session id
     (`sess-<32 hex>`), registers it in the relay, and sends an `Open`
     control downlink to the agent (fps is a hint: the server accepts 0-30,
     where 0 = agent default; the web viewer currently requests 2).
   - It mints a **stream ticket** (bound to device + session + operator,
     valid ~5 minutes) and returns it in the response.
   - Audited as `session.started` (who, which device, which session).
   - If the agent is offline (no live downlink), the start fails with `502`
     and no session is left registered.

2. **Stream** — `GET /api/devices/{id}/session/stream?ticket=...`
   - Authenticated by the **stream ticket**, not the operator JWT (the JWT
     would otherwise have to travel in the URL query string and leak into
     access logs). The ticket is multi-use within its TTL, so the browser's
     EventSource auto-reconnects after transient drops.
   - Emits **named SSE events** (see [Viewer transport](#viewer-transport-sse)).
   - Sends a `: ping` keepalive every 15 s so proxies and the browser don't
     drop a stalled connection.
   - Audited as `session.stream_opened` / `session.stream_closed` (with
     duration).

3. **Active** — the agent streams frames at the configured rate
   - The relay keeps only the **latest** frame per session (drop-old) to
     prevent backpressure
   - Every connected viewer receives frames via SSE
   - If the device goes offline mid-session, viewers get an
     `agent_offline` status frame and the session stays resumable

4. **Close** — `POST /api/devices/{id}/session/stop`
   - The server sends a `Close` control downlink, clears the session state
     (including the latest frame, so a stale frame can't be replayed to a
     new session), and audits `session.stopped` (with duration).
   - Stopping a session id that isn't the active one returns `404`.

5. **Auto-close** — when the **last** viewer disconnects, the relay fires
   `OnAutoClose` and the server sends a `Close` downlink, so an abandoned
   viewer tab doesn't leave the agent capturing indefinitely.

6. **Sweep** — a background loop (30 s) reaps completed/stale file-pull
   transfers and idle device state, bounding memory on long-running servers.

## Viewer transport (SSE)

The browser opens the stream with `EventSource` and listens for **named**
events. This matters: named SSE events (`event: <kind>`) do **not** trigger
`onmessage` — the viewer must `addEventListener` for each kind, which is why
the frontend listens for `hello` / `frame` / `status` / `goodbye` explicitly.

### Stream ticket

- Minted at session start, returned as `stream_ticket` in the start response.
- Bound to the **device + session + operator** that started it, and valid for
  ~5 minutes (lazily garbage-collected as new tickets are minted).
- Presented as `?ticket=...` on the stream URL; the operator JWT stays in the
  `Authorization` header. A ticket alone is useless without a valid operator
  session, so a leaked URL in a proxy log exposes only a short-lived, scoped
  handle — not the long-lived JWT.
- Multi-use within its TTL, so `EventSource` reconnection after a transient
  network drop succeeds.

### Event kinds and wire format

Each event is `event: <kind>` + a single `data:` line of JSON. The JSON uses
**snake_case** fields matching the rest of the API (the frontend reads
`data.kind`, `data.jpeg_b64`, `data.status`, …):

| `event:`  | When | Payload fields |
|-----------|------|----------------|
| `hello`   | Stream established | `kind`, `session_id` |
| `frame`   | Each captured frame (latest-only) | `kind`, `session_id`, `seq`, `codec`, `width`, `height`, `jpeg_b64` (base64 JPEG), `capture_ts_ms` |
| `status`  | Capture status change | `kind`, `session_id`, `status` (`vnc_required` / `unavailable` / `agent_offline`) |
| `goodbye` | Server closed the session (auto-close on last-viewer disconnect, or explicit stop) | `kind`, `session_id` |

Comment lines (`: connected` on open, `: ping` every 15 s) are SSE keepalives
and carry no payload.

### Audit trail

Every session event is published on the flow bus under the subject
`ourway-rmm.events.session`, journaled and fanned out to the `/events`
stream. The events recorded are:

- `session.started` — `user`, `session_id`, `fps`
- `session.stream_opened` — `user`, `session_id`
- `session.stream_closed` — `user`, `duration_ms`
- `session.stopped` — `user`, `session_id`, `duration_ms`

This is the compliance answer to "who had remote access to this device, and
when".

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OURWAY_RMM_SESSION_SOURCE` | (platform default) | Capture backend override. Use `"test"` for synthetic animated frames (useful for headless testing). |

## Platform-Specific Implementation

Input injection is a phase 2 feature. Only the **Windows** backend is fully
implemented today; Linux and Darwin are explicit stubs that return
`errNotImplemented` (they never fake success, so the server can surface the
real cause rather than silently dropping input).

### Windows Input (input_windows.go) — implemented

Uses the Windows SendInput API via `golang.org/x/sys/windows`:

- Mouse coordinates are normalized to 0-65535 range (SendInput's absolute space)
- Keyboard events use `VkKeyScanW` to map Unicode codepoints to virtual-key codes
- Each event is sent synchronously; SendInput's return value is verified
- Modifier keys are sent before/after character events

### Linux Input (input_linux.go) — stub

Planned to use X11's XTest extension via `xdotool`. Currently returns
`errNotImplemented` for all events.

### Darwin Input (input_darwin.go) — stub

Planned to use CGEvent via AppleScript/`osascript`. Currently returns
`errNotImplemented` for all events.

## Testing

### Agent unit tests

- `agent/internal/session/session_test.go` — driver input routing + safety
  with a mock relayer
- Tests verify:
  - Mouse/keyboard events are routed to the input relayer only when a
    matching session is live (otherwise dropped)
  - Any key event with the Meta/Win bit held is blocked (Win+L / Win+R /
    Win+X / Start-menu sequences)
  - A failed downlink send clears the zombie session (no capture leak)
  - Relayer errors are logged (not propagated)
  - FPS clamping logic

### Server tests

- `server/internal/sessionrelay/sessionrelay_test.go` — latest-only relay,
  auto-close on last-viewer disconnect, stale/closed-frame drop, pull-cap
  enforcement, device-mismatch on pulls, sweep behavior, close-clears-state
- `server/internal/httpapi/domain_session_test.go` — HTTP contract:
  - **`TestSessionStreamDeliversNamedSSEEvents`** is the end-to-end proof of
    the critical fix: a frame pushed through the relay arrives on the stream
    as a named `event: frame` with the exact snake_case JSON the frontend
    reads (`kind`, `codec`, `width`, `height`, `seq`, `jpeg_b64`)
  - Stream ticket enforcement (no ticket / garbage ticket → 403)
  - Tenant scoping (a tech scoped to one client cannot open a session on
    another client's device)
  - Start/stop downlink the correct `Open`/`Close` controls and emit
    `session.started` / `session.stopped` audits
  - Mismatched stop id → 404; offline agent → 502 with no dangling session

### Integration Testing

- Set `OURWAY_RMM_SESSION_SOURCE=test` to use the synthetic animated backend
- The test backend produces deterministic animated frames that can be verified
- Works on any platform (pure Go, no cgo)

## Phase 2 Input

Phase 2 turns the view-only session into two-way remote control by relaying
browser input to the agent as `MouseEvent` / `KeyboardEvent` downlink frames
(the proto messages already exist). The agent-side relayer, input gating,
and Win/Meta blocking are already in place and unit-tested; the remaining
work is the server-side input endpoint + the frontend input capture UI.

Also planned: multi-monitor support, session recording, and co-viewer mode
(multiple operators viewing simultaneously).

## Operator View

For end-user operator documentation on using remote sessions, see
[Remote Session — Operator Guide](remote-session-operator.md).
