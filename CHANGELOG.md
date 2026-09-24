# Changelog

## [Unreleased]

### Fixed

- **CI lint red on main**: four session files were unformatted (gofmt), and
  `buf lint` failed on the rebrand's hyphenated proto directory vs
  underscored package mismatch (`PACKAGE_DIRECTORY_MATCH` is now exempted
  in `buf.yaml` — the mismatch is intentional).
- **CI Test + Installer e2e could never start their NATS service**:
  nats-server flags (`--jetstream`) were placed in the service `options`
  (docker-create flags only; service containers have no `command` key),
  failing with `unknown flag: --jetstream`. The `nats:2.10-alpine` image's
  default config already enables JetStream + the 8222 monitor port, so only
  the healthcheck remains in options.
- **Live Postgres tests could not create their scratch databases**: the
  scratch db name contains a hyphen (`ourway-rmm_...`), but `CREATE
  DATABASE`/`DROP DATABASE` interpolated it unquoted, so every live test
  failed with `syntax error at or near "-"` (SQLSTATE 42601). The
  identifiers are now double-quoted in all 19 affected call sites.
- **Stale migration count in store tests**: five live tests asserted
  exactly 11 migrations applied, but the suite now ships 16
  (0012–0016 added after the tests were written). They now count the
  `.sql` files in `server/migrations` instead of hardcoding.

## [1.4.0] - 2026-09-24

### Added

- **Remote session audit trail**: every session event (`session.started`,
  `session.stream_opened`, `session.stream_closed`, `session.stopped`) is now
  published on the flow bus (subject `ourway-rmm.events.session`) — the
  compliance answer to "who had remote access to which device, and when".
- **Remote session lifecycle safety**: the server auto-closes a session when
  the last viewer disconnects, a background sweep reaps stale file-pull
  transfers and idle device state, and an `agent_offline` status is surfaced
  to viewers when the device drops (the session is kept and resumes on
  reconnect). Closing a session now also clears its latest frame so a stale
  frame can't be replayed to a new session.
- **Remote session hardening**: session streams are now opened with a
  short-lived, device+session+operator-bound **stream ticket** (the operator
  JWT no longer appears in the stream URL), session IDs are cryptographically
  random, all four session routes are tenant-scoped to the operator's clients,
  and agent input is gated to the live session with Win/Meta-key combos
  blocked (no remote `Win+L`/`Win+R`/`Win+X`).

### Changed

- **Windows service + install dir renamed (rebrand follow-through)**: the
  Windows service is now `OurWayRMMAgent` (was `RmmWayAgent`), non-elevated
  installs go to `%LOCALAPPDATA%\OurWayRMM` (was `RmmWay`), and elevated
  installs to `C:\Program Files\OurWay RMM` (was `RMMWay`). Re-running
  `scripts/install.ps1` on a pre-1.3.0 machine stops + deletes the legacy
  service and carries the old install dir over, so an upgrade can't leave
  two agents running or orphan the bootstrap config.
- **Version constants aligned to v1.3.0**: the repo's version pins (Makefile
  `VERSION`, `.env.prod.example`, the server's built-in default, and the
  compose image tags + `OURWAY_RMM_VERSION` pins) now all say 1.3.0 — release
  deployments previously shipped under the v1.3.0 tag while the server
  self-reported 1.0.2.
- **Release asset inventory**: the CI inventory check now includes the
  windows-arm64 agent binary and its SBOM (the upload loop already shipped
  them; the check didn't verify them).
- **Comment hygiene**: removed the last references to the long-deleted
  `IDEA.md` from code comments (logship, `logs.proto` + generated stub,
  migrations 0001/0007, store, baseline).

### Fixed

- **Remote session viewer rendered nothing (critical)**: the server emits
  *named* SSE events (`event: frame`), which never trigger `onmessage` — and
  the viewer read PascalCase JSON fields the server never emitted. The viewer
  now listens with `addEventListener` for `hello`/`frame`/`status`/`goodbye`
  and reads the server's snake_case payload, so frames actually render. The
  server's frame events now carry explicit snake_case JSON tags. An
  end-to-end test (`TestSessionStreamDeliversNamedSSEEvents`) proves a frame
  pushed through the relay arrives as a named `event: frame` with the exact
  fields the frontend reads.

- **Enrollment over gRPC (rebrand regression)**: the ingest JWT interceptor's
  Enroll bypass compared against a hand-typed full method path with a hyphen
  (`ourway-rmm`), while the generated proto service name uses an underscore
  (`ourway_rmm`), so every gRPC `Enroll` was rejected with 401 (the plain-gRPC
  fallback of the bootstrap flow was broken). The interceptor now uses the
  generated `AgentService_Enroll_FullMethodName` constant.
- **Self-heal escalations panicked**: ticket creation on escalation passed a
  nil context to the Postgres ticket store (pgx dereferences the context and
  panics). Now passes `context.Background()`.
- **Broken `go test` after rebrand**: the signed update test fixture still
  carried the pre-rebrand signature comment, failing `TestVerifySignatureValid`.
  The fixture was re-signed with a fresh throwaway test key (regeneration
  steps documented in the test).
- **Broken load-test module after rebrand**: `scripts/load-test/go.mod`
  still required the old `rmmway/proto/gen` module path; it no longer
  compiled. Fixed + the committed `load-test` binary artifact was removed
  (now gitignored).
- **Remote session test suite was never committed**: the 419-line
  `domain_session_test.go` (incl. `TestSessionStreamDeliversNamedSSEEvents`,
  cited by docs/remote-session.md) was untracked in git; it is now committed.
- **Stray signer binary in the tree**: a 3.4 MB compiled `tools/signer/signer`
  ELF was sitting untracked; removed and added to .gitignore so it can't recur.
- **Doc drift**: the operator guide's bootstrap API section now documents the
  actual `{bootstrap_token, device_id}` response shape, and install.ps1's
  stale v0.4.0 hot-swap comment no longer pins a release tag.

### Removed

- Deprecated/unused code: `tls.Config.PreferServerCipherSuites` (ignored by
  Go since 1.18), the superseded `requireOperator`/`requireOperatorStream`/
  `bearerToken` HTTP gates (replaced by the RBAC route gates), unused types,
  helpers, test scaffolding, and unused frontend UI-kit components
  (`Spinner`, `Tooltip`, `SegmentedControl`), plus an orphaned temp CSS file.

## [1.3.0] - 2026-09-14
- Rebrand: RMMWay → OurWay RMM across the entire codebase
- All binaries, Docker images, and release assets now use the `ourway-rmm` name
- Environment variables renamed from `RMMWAY_*` to `OURWAY_RMM_*`
- Proto package renamed from `rmmway.agent.v1` to `ourway_rmm.agent.v1`
- Updated all documentation, configuration files, and deployment manifests

## [1.2.0] - 2026-09-14

### Added

- **Process management** — list running processes with CPU/memory stats, filter by name, and kill processes by PID
- **Service management** — list system services with status, and control services (start/stop/restart)
- Remote control capabilities now support process and service operations across Linux, Windows, and macOS
- New W3-3 capability tokens for process/service management (ourway-rmm.list_processes, ourway-rmm.kill_process, ourway-rmm.list_services, ourway-rmm.service_control)
- Updated release compose files to reference v1.2.0 images

## [1.0.1] - 2026-09-10

### Fixed

- Frontend retry logic on first boot: the initial `/api/setup/status` call now retries up to 10 times with a 2-second delay before falling back to degraded mode. This fixes the issue where on first boot, the frontend loads before the backend finishes initializing (migrations, service startup) and the UI shows the Login screen instead of the Setup wizard.

## [1.0.0] - 2026-09-10

### Added

- **Fleet dashboard** with per-client summary tiles (online, alerts, uptime, patch compliance)
- **Mobile-responsive UI** — the entire operator interface is usable on phones and tablets
- **Scheduled and compliance reports** in CSV and PDF:
  - Fleet status
  - Device report
  - Patch compliance
  - License compliance
  - Uptime/SLA
- **Maintenance windows** — pause alerting for devices, tags, or clients during planned work
- **Deep inventory collection** — hardware, software, services, users, domain membership
- **Patch management** — Windows Update query/approve/apply with third-party software support
- **Remote session** — live screen viewing of managed devices (view-only in v1.0.0)
- **User management & RBAC** — multiple operator accounts with role-based access and TOTP MFA
- **OIDC support** — Okta/Auth0/Keycloak SSO (any OpenID Connect identity provider)
- **Ticketing** — alert-to-ticket escalation with assignment, SLA tracking, and resolution workflows
- **Multi-channel notifications** — email, Slack, Teams, PagerDuty with per-client routing policies
- **Cron flow triggers** — schedule automation runs
- **Load test harness** for verifying 5,000-device scale

### Changed

- Version bumped from 0.1.0 to 1.0.0
- Operator guide added (`docs/operator-guide.md`)
- Remote session documentation added (`docs/remote-session.md`)
- Release process documented (`RELEASE.md`)
- v1.0.0 release notes added (`docs/releases/v1.0.0.md`)

### Fixed

- Install.sh mTLS address derivation (both case branches strip scheme first)
- Device search index refresh on enroll and stream open

## [0.1.0] - 2026-09-08

### Added

- Initial release with fleet monitoring, dynamic baselining, alerts
- Agent enrollment with one-time tokens and mTLS
- Command dispatch, file transfer
- Self-healing playbooks and flow automation
- Client/tenant (MSP) model
- Webhooks, SSE events, client export
- Settings and profile pages

[1.4.0]: https://github.com/welcometotheweb/ourway-rmm/releases/tag/v1.4.0
[1.3.0]: https://github.com/welcometotheweb/ourway-rmm/releases/tag/v1.3.0
[1.2.0]: https://github.com/welcometotheweb/ourway-rmm/releases/tag/v1.2.0
[1.0.1]: https://github.com/welcometotheweb/ourway-rmm/releases/tag/v1.0.1
[1.0.0]: https://github.com/welcometotheweb/ourway-rmm/releases/tag/v1.0.0
[0.1.0]: https://github.com/welcometotheweb/ourway-rmm/releases/tag/v0.1.0
