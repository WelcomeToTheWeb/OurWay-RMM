import { useEffect, useRef, useState, useCallback } from "react";
import { api } from "./api.js";
import { Button, Icon, Banner } from "./ui/index.js";
import "./views/session-viewer.css";

// SessionViewer renders a live remote session for one device.
// Connects to the SSE stream and renders frames on a canvas.
//
// SSE protocol (server-side named events — `onmessage` would NEVER fire for
// them, which is why the viewer used to render nothing):
//   event: hello    — stream established, carries the session id
//   event: frame    — a captured JPEG (jpeg_b64, width, height, seq)
//   event: status   — capture status (vnc_required, agent_offline, …)
//   event: goodbye  — the server closed the session (auto-close on last
//                     viewer, or another operator stopped it)
export default function SessionViewer({
  deviceID,
  deviceName,
  token,
  onClose,
  onUnauthorized,
}) {
  const canvasRef = useRef(null);
  const ctxRef = useRef(null);
  const streamRef = useRef(null);
  const sessionIDRef = useRef(null);
  // statusRef mirrors `status` for handlers that would otherwise capture a
  // stale closure (the onerror handler closes the EventSource and checks
  // whether we're already stopped).
  const statusRef = useRef("connecting");
  const stoppedRef = useRef(false);

  const [status, setStatus] = useState("connecting");
  const [error, setError] = useState(null);
  const [connected, setConnected] = useState(false);
  const [frameCount, setFrameCount] = useState(0);
  const [lastFrameTime, setLastFrameTime] = useState(null);

  const updateStatus = useCallback((next) => {
    statusRef.current = next;
    setStatus(next);
  }, []);

  // Connect to the SSE stream when mounted.
  useEffect(() => {
    if (!canvasRef.current) return;
    ctxRef.current = canvasRef.current.getContext("2d");

    let cancelled = false;

    // Start the session via API. The response carries a short-lived stream
    // ticket: the EventSource presents it instead of the operator JWT (the
    // JWT in the URL would leak into access logs).
    api
      .startSession(token, deviceID, { fps: 2 })
      .then((res) => {
        if (cancelled) return;
        sessionIDRef.current = res.session_id;
        connectStream(res.stream_ticket);
      })
      .catch((e) => {
        if (cancelled) return;
        if (e.unauthorized) onUnauthorized();
        else setError(e.message);
      });

    function connectStream(ticket) {
      const evtSource = new EventSource(
        `/api/devices/${deviceID}/session/stream?ticket=${encodeURIComponent(ticket)}`,
      );
      streamRef.current = evtSource;

      evtSource.onopen = () => {
        if (cancelled) return;
        setConnected(true);
      };

      // Named events: the server sends `event: <kind>` frames, which do NOT
      // trigger onmessage — only addEventListener(kind) does.
      evtSource.addEventListener("hello", (evt) => {
        if (cancelled) return;
        try {
          const data = JSON.parse(evt.data);
          if (data.session_id) sessionIDRef.current = data.session_id;
          updateStatus("streaming");
        } catch (e) {
          // Ignore parse errors.
        }
      });

      evtSource.addEventListener("frame", (evt) => {
        if (cancelled) return;
        try {
          const data = JSON.parse(evt.data);
          renderFrame(data);
          setFrameCount((c) => c + 1);
          setLastFrameTime(new Date());
        } catch (e) {
          // Ignore parse errors.
        }
      });

      evtSource.addEventListener("status", (evt) => {
        if (cancelled) return;
        try {
          const data = JSON.parse(evt.data);
          if (data.status === "vnc_required") {
            updateStatus("vnc_required");
            setError("Headless device — open a local VNC session");
          } else if (data.status === "agent_offline") {
            // The agent's uplink dropped mid-session: the server keeps the
            // session alive and re-opens capture when it reconnects, but the
            // viewer must know the screen is frozen in the meantime.
            updateStatus("agent_offline");
          } else if (statusRef.current === "agent_offline") {
            // A fresh frame (or cleared status) means the agent is back.
            updateStatus("streaming");
          }
        } catch (e) {
          // Ignore parse errors.
        }
      });

      evtSource.addEventListener("goodbye", () => {
        // The server closed the session (last viewer auto-close on another
        // tab, or another operator stopped it). Mark stopped WITHOUT calling
        // the stop API again — the session is already gone.
        if (cancelled || stoppedRef.current) return;
        stoppedRef.current = true;
        evtSource.close();
        setConnected(false);
        updateStatus("stopped");
        onClose();
      });

      evtSource.onerror = () => {
        if (cancelled) return;
        evtSource.close();
        setConnected(false);
        // statusRef: the plain `status` state here would be stale
        // (captured at connect time).
        if (statusRef.current !== "stopped") {
          updateStatus("disconnected");
        }
      };
    }

    function renderFrame(data) {
      if (!data.jpeg_b64 || !ctxRef.current) return;

      // Decode base64 JPEG and render to canvas. Resize only when the
      // resolution changes — setting canvas.width on every frame resets the
      // context state and forces a full reallocation at 30fps.
      const img = new Image();
      img.onload = () => {
        if (!ctxRef.current || !canvasRef.current) return;
        const canvas = canvasRef.current;
        const w = data.width || img.width;
        const h = data.height || img.height;
        if (canvas.width !== w) canvas.width = w;
        if (canvas.height !== h) canvas.height = h;
        ctxRef.current.drawImage(img, 0, 0, w, h);
        if (statusRef.current === "agent_offline") {
          updateStatus("streaming");
        }
      };
      img.src = "data:image/jpeg;base64," + data.jpeg_b64;
    }

    return () => {
      cancelled = true;
      if (streamRef.current) {
        streamRef.current.close();
      }
      if (sessionIDRef.current && !stoppedRef.current) {
        api
          .stopSession(token, deviceID, { session_id: sessionIDRef.current })
          .catch(() => {});
      }
      // Note: closing the EventSource is also the server-side signal that
      // the last viewer left — the server auto-closes the session (agent
      // stops capturing), so no stop call is strictly required here.
    };
  }, [deviceID, token, onClose, onUnauthorized, updateStatus]);

  const stopSession = useCallback(async () => {
    if (stoppedRef.current) {
      onClose();
      return;
    }
    stoppedRef.current = true;
    updateStatus("stopped");
    if (streamRef.current) {
      streamRef.current.close();
    }
    if (sessionIDRef.current) {
      try {
        await api.stopSession(token, deviceID, {
          session_id: sessionIDRef.current,
        });
      } catch (e) {
        if (e.unauthorized) onUnauthorized();
        // 404 = the server already auto-closed it; either way, on our way
        // out.
      }
    }
    onClose();
  }, [token, deviceID, onClose, onUnauthorized, updateStatus]);

  const statusLabel = {
    connecting: "Connecting…",
    streaming: "Streaming",
    disconnected: "Disconnected",
    stopped: "Stopped",
    vnc_required: "VNC Required",
    agent_offline: "Agent offline",
  };

  return (
    <div className="session-viewer">
      <div className="session-header">
        <div className="session-info">
          <h2>
            <Icon name="monitor" /> Remote Session
          </h2>
          <p className="session-device">{deviceName || deviceID}</p>
        </div>
        <div className="session-controls">
          <span
            className={
              "session-status " + (status === "streaming" ? "live" : "")
            }
          >
            {statusLabel[status] || status}
          </span>
          {connected && (
            <span className="session-frames">
              {frameCount} frames
              {lastFrameTime && ` (${timeAgo(lastFrameTime)})`}
            </span>
          )}
          <Button variant="ghost" onClick={stopSession}>
            <Icon name="power" /> End Session
          </Button>
        </div>
      </div>

      {error && (
        <Banner tone="err" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      {status === "agent_offline" && (
        <Banner tone="info">
          The device's uplink dropped — the screen is frozen. Waiting for the
          agent to reconnect (capture resumes automatically).
        </Banner>
      )}

      <div className="session-canvas">
        {status === "connecting" ? (
          <div className="canvas-placeholder">
            <p>Waiting for stream…</p>
          </div>
        ) : (
          <canvas
            ref={canvasRef}
            className="session-screen"
            width={1024}
            height={768}
          />
        )}
      </div>

      <div className="session-footer">
        <p className="session-note">
          Phase 1: view-only screen capture (JPEG over SSE, drop-oldest).
          Interactive input is not wired in this phase — see the remote
          session docs for the phase 2 plan.
        </p>
      </div>
    </div>
  );
}

function timeAgo(date) {
  const diff = Date.now() - date.getTime();
  if (diff < 1000) return "just now";
  if (diff < 60000) return Math.floor(diff / 1000) + "s ago";
  return Math.floor(diff / 60000) + "m ago";
}
