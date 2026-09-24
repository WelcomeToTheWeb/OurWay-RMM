# proto/ — shared gRPC protocol definitions

Agent ↔ server protocol — fully shipped: **W0-2** delivered the `.proto`
files + codegen, **W1-5** the server side, **W1-4** the agent side.

## Layout

```
proto/
  ourway-rmm/agent/v1/
    agent.proto      # AgentService: Enroll + Stream (bidi uplink) + RefreshLeaf
                     # (mTLS leaf cert rotation). StreamRequest oneof: Heartbeat,
                     # MetricBatch, CommandResult, LogBatch, SessionFrame, FileChunk,
                     # InventoryReport, PatchStatus. StreamResponse oneof:
                     # HeartbeatAck, Command, SessionControl, FileChunk.
    metrics.proto    # MetricBatch / Metric (the five W1-2 metric families)
    commands.proto   # Command dispatch (RunScript, Reboot; W3-3 capability tokens)
    logs.proto       # LogEntry / LogBatch: structured agent log events pushed on
                     # the Stream uplink (W6-1; the agent also ships them to Loki)
  gen/               # generated Go (committed; `make proto` regenerates)
    go.mod           # module github.com/welcometotheweb/ourway-rmm/proto/gen
```

## Usage

```sh
make proto       # buf generate -> proto/gen/ + go mod tidy
make proto-lint  # buf lint
```

Both `server/` and `agent/` import the stubs via
`replace github.com/welcometotheweb/ourway-rmm/proto/gen => ../proto/gen`.

## Auth model

- **Enroll**: one-time bootstrap token (from the `curl|sh` line) → device_id +
  long-lived agent JWT. Never called again after first success (W1-4).
- **Stream**: every RPC carries `Authorization: Bearer <jwt>`; W1-5 verifies,
  W3-1 layers mTLS on top, W3-3 adds per-command capability tokens.
- **Dedup**: metrics dedupe on `(device_id, name, source, timestamp_ms)`,
  making offline replay at-least-once and idempotent: a re-sent batch after a
  reconnect is a no-op, because every entry is already stored under its key.
