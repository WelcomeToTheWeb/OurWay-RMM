# Remote Session — Operator Guide

OurWay RMM remote sessions provide live screen viewing of managed devices.
Phase 1 (v1.0.0) supports view-only sessions; phase 2 adds two-way
mouse/keyboard input.

## Opening a Session

1. Navigate to the **Devices** list
2. Open the target device's **detail** page
3. Click the **🔌 Connect** button in the device's quick actions

The session viewer opens (in the same tab). The agent begins capturing screen
frames at approximately 2 frames per second (the start API accepts 0–30 fps,
where 0 = agent default; the viewer currently requests 2).

## Session Viewer Controls

- **End Session:** The **End Session** button in the viewer header stops the
  session and closes the viewer.
- **Navigate away:** Closing the viewer also ends the session (the server
  auto-closes when the last viewer disconnects).
- **Frame counter:** The header shows how many frames have rendered and how
  long ago the last one arrived — a useful staleness indicator.

## Status Indicators

The viewer's status pill shows one of the following:

| Status | Meaning |
| ------ | ------- |
| **Connecting…** | Waiting for the stream / first frame |
| **Streaming** | Frames are being received and rendered |
| **Disconnected** | The stream dropped (the browser auto-retries) |
| **Stopped** | The session was ended |
| **VNC Required** | Device has no local display; open a local VNC session on the device to capture its screen |
| **Agent offline** | The agent's connection dropped. The session is **kept** — it resumes automatically when the agent reconnects. |

(A capture-backend error surfaces as an `unavailable` status.)

## Session Duration

- A session ends when **you end it** (End Session button) or when you navigate
  away from the viewer.
- **Auto-close:** if the last viewer disconnects, the server closes the
  session, so an abandoned view doesn't leave the agent capturing forever.
- **Agent reconnects:** if the device goes offline and back, an open session
  resumes (you'll briefly see **Agent offline**, then frames return).
- Long-running sessions (hours) are supported but consume device resources.

## Permissions

Opening a session is a **role** action, not a capability flag:

- **admin** and **tech** roles can open sessions.
- **viewer** (and any other role) cannot.
- In addition, a non-admin can only open a session on a device that belongs
  to a **client they can see** (tenant scoping). A tech scoped to Client A
  cannot remotely control a device in Client B.

Every start/stop/view is **audited** (who, which device, when, how long), so
remote access is traceable after the fact.

## Security Model

- Screen frames travel over the same **mTLS gRPC** stream as heartbeats
- Frames are dropped-old (only the latest is relayed) to prevent backpressure
- **Session IDs** are cryptographically random and echoed in every frame for
  validation
- **Stream ticket:** the browser opens the frame stream with a short-lived
  ticket (not your session token), so your long-lived token is never written
  to the URL and can't leak into proxy/access logs
- **Tenant scoping:** an operator can only view/control devices in clients
  they're granted
- **Auditable:** every session event is journaled and available in the audit
  trail
- No frame or session data is persisted to disk

## Troubleshooting

### No frames appear

1. Verify the agent is online (check the device list)
2. Check for the `vnc_required` status (headless Linux devices need a local VNC session)
3. Check agent logs for capture backend errors
4. Verify network connectivity between agent and server

### Frames are stale or slow

1. The viewer only displays the latest frame; stale frames indicate network issues
2. Check device CPU usage (high load slows capture)
3. Try reducing the frame rate

### The screen is frozen and shows **Agent offline**

1. The device's uplink dropped (network blip, agent restart, device sleep).
2. The session is **kept** — no action needed; frames resume automatically when
   the agent reconnects.
3. If it stays offline, check the device is online and the agent is running.

### Capture backend errors

Common errors:

- `no display found` (Linux headless) → open local VNC session
- `capture timeout` → device under heavy load
- `permissions` (macOS) → grant screen recording permission to the agent

On macOS, the agent requires Screen Recording permission in System Preferences → Privacy & Security → Screen Recording.

## Phase 2 Roadmap

The following features are planned for phase 2:

- **Mouse click and movement input** — the agent-side input relay, session-id
  gating, and Win/Meta-key blocking are already implemented and tested; what
  remains is the server-side input endpoint and the frontend input-capture UI
- **Keyboard input** — same status as mouse input (agent ready, server + UI
  to wire)
- Multi-monitor support
- Session recording
- Co-viewer mode (multiple operators viewing simultaneously)

---

*Remote session documentation, v1.0.0 release.*
