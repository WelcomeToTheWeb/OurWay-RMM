# OurWay RMM Operator Guide

This guide covers operating an OurWay RMM installation in production:
installation, configuration, device enrollment, client management,
alerting, reporting, patch management, and maintenance.

## Contents

- [Installation](#installation)
- [Configuration](#configuration)
- [Device Enrollment](#device-enrollment)
- [Client/Tenant Management](#clienttenant-management)
- [User Management & RBAC](#user-management--rbac)
- [Alerting & Flow Automation](#alerting--flow-automation)
- [Ticketing & Notifications](#ticketing--notifications)
- [Reporting & Compliance](#reporting--compliance)
- [Patch Management](#patch-management)
- [Monitoring & Maintenance](#monitoring--maintenance)
- [Backup & Disaster Recovery](#backup--disaster-recovery)
- [Security Hardening](#security-hardening)

---

## Installation

### System Requirements

- Linux server (Debian 12+ or Ubuntu 22.04+ recommended)
- 4 CPU cores minimum
- 8GB RAM minimum (16GB recommended)
- 100GB SSD storage minimum
- Docker 24+ or Go 1.26+ for building from source

### Quick Start (Docker)

```sh
# Clone the repo
git clone https://github.com/welcometotheweb/ourway-rmm
cd ourway-rmm

# Copy and edit the production environment file
cp .env.prod.example .env.prod
# Edit .env.prod with your secrets

# Build and start
make prod
```

The server will be accessible at the URL you configured in `OURWAY_RMM_PUBLIC_URL`.

### First-Boot Setup

After the stack starts, visit the web UI and complete the first-boot wizard:

1. Create your root admin account
2. Set the organization name (stamped into the auto-generated mTLS root CA)
3. Configure the SMTP outbox (or skip for now)

### Verifying Installation

```sh
# Check health
curl -fsS https://rmm.example.com/healthz

# Check server logs
docker compose --env-file .env.prod -f docker-compose.prod.yml logs server --tail 50
```

---

## Configuration

### Environment Variables

All configuration is via environment variables in `.env.prod`. Key variables:

| Variable | Purpose | Example |
| -------- | ------- | ------- |
| `OURWAY_RMM_PUBLIC_URL` | Public operator URL | `https://rmm.example.com` |
| `OURWAY_RMM_JWT_SECRET` | JWT signing secret | `openssl rand -hex 32` |
| `OURWAY_RMM_PG_PASSWORD` | Postgres password | `openssl rand -hex 16` |
| `OURWAY_RMM_MEILI_MASTER_KEY` | Meilisearch key | `openssl rand -hex 16` |
| `OURWAY_RMM_MINIO_PASSWORD` | MinIO password | `openssl rand -hex 16` |
| `OURWAY_RMM_ADMIN_USER` | Admin username | `admin` |
| `OURWAY_RMM_ADMIN_PASSWORD` | Admin password | `openssl rand -hex 16` |
| `OURWAY_RMM_AGENT_MTLS_PORT` | Agent mTLS port | `50052` |

### Agent Configuration

Agents are configured via environment variables or a config file:

| Variable | Purpose | Example |
| -------- | ------- | ------- |
| `OURWAY_RMM_SERVER` | server URL (HTTPS origin) | `https://rmm.example.com` |
| `OURWAY_RMM_BOOTSTRAP_TOKEN` | one-time enrollment token (first boot only; stripped from the config file after use) | `<token>` |
| `OURWAY_RMM_GRPC_ADDR` | plain gRPC bootstrap endpoint override (default: server host, port 50051) | `rmm.example.com:50051` |
| `OURWAY_RMM_GRPC_MTLS_ADDR` | mTLS gRPC endpoint override (default: server host, port 50052) | `rmm.example.com:50052` |
| `OURWAY_RMM_IDENTITY` | persisted enrollment identity path | `~/.ourway-rmm/agent-identity.json` |
| `OURWAY_RMM_LOG_FILE` | JSON-lines log path | `/var/log/ourway-rmm-agent.jsonl` |
| `OURWAY_RMM_SERVICES` | comma-separated services to sample (unset = no service sampling) | `nginx,postgresql` |
| `OURWAY_RMM_AUTO_UPDATE` | set `off`/`0` to disable signed auto-update (on by default when a valid `OURWAY_RMM_UPDATE_PUBKEY` is configured) | `off` |
| `OURWAY_RMM_UPDATE_INTERVAL` | auto-update check cadence | `15m` |
| `OURWAY_RMM_UPDATE_PUBKEY` | pinned minisign public key for release verification (auto-update is a no-op without it) | `<pubkey>` |
| `OURWAY_RMM_CERT_DIRS` | TLS certificate dirs to scan (default `/etc/ssl/certs`) | `/etc/letsencrypt/live` |
| `OURWAY_RMM_EVENTLOG` | OS event-log tailing (journal / Windows Event Log / unified log); `off` disables | `off` |

---

## Device Enrollment

### One-Time Token Enrollment

1. In the web UI, click "Add Device"
2. Choose the operating system
3. Copy the generated command
4. Run the command on the target device

```sh
# Linux example (the UI's "Add Device" dialog shows the same command)
curl -fsSL https://raw.githubusercontent.com/welcometotheweb/ourway-rmm/main/scripts/install.sh | bash -s -- --server https://rmm.example.com --bootstrap <token>
```

### Bootstrap Token API

For programmatic enrollment, mint a bootstrap token:

```sh
curl -X POST \
  -H "Authorization: Bearer <operator-token>" \
  https://rmm.example.com/api/bootstrap
```

The response contains a one-time token to use with the agent enrollment command.

### Verifying Enrollment

After enrollment, the device should appear in the device list as "online"
within 60 seconds. Verify by checking:

```sh
# List all devices
curl -s -H "Authorization: Bearer <token>" \
  https://rmm.example.com/api/devices | jq '.[] | {id, hostname, online}'
```

---

## Client/Tenant Management

### Creating a Client

```sh
curl -X POST \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"name":"Acme Corp","description":"Enterprise client, 123 Main St"}' \
  https://rmm.example.com/api/clients
```

### Assigning Devices to a Client

```sh
curl -X PATCH \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"client_id":"<client-id>"}' \
  https://rmm.example.com/api/devices/<device-id>/client
```

### Scoping Queries by Client

```sh
# List devices for a specific client
curl -s -H "Authorization: Bearer <token>" \
  "https://rmm.example.com/api/devices?client=<client-id>"

# List alerts for a specific client
curl -s -H "Authorization: Bearer <token>" \
  "https://rmm.example.com/api/alerts?client=<client-id>"
```

---

## User Management & RBAC

### Creating Users

```sh
curl -X POST \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"username":"tech","password":"secret","role":"tech"}' \
  https://rmm.example.com/api/users
```

### Available Roles

| Role | Permissions |
| ---- | ----------- |
| `admin` | Full access to all features |
| `tech` | Device management, alerts, tickets |
| `viewer` | Read-only access to dashboards and reports |

### MFA Setup

Users with MFA enabled can set up TOTP in their profile. Use any TOTP app
(Authy, Google Authenticator) to scan the QR code provided during setup.

---

## Alerting & Flow Automation

### Alert Rules

Alerts are generated automatically by the dynamic baselining engine. No
thresholds to configure — the engine learns each device's normal behavior
and alerts on deviations.

### Viewing Alerts

```sh
curl -s -H "Authorization: Bearer <token>" \
  "https://rmm.example.com/api/alerts?status=open" | jq '.'
```

### Acknowledging an Alert

```sh
# Alerts transition open -> acked -> resolved; PATCH with the target
# status ("acked" or "resolved"). Re-opening is refused.
curl -X PATCH \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"status":"acked"}' \
  https://rmm.example.com/api/alerts/<alert-id>
```

### Flow Automation

Flows are event-driven automation chains. Create a flow via the UI or API.

Example flow (the body is `{name, description, graph, cooldown_seconds?, enabled?}`,
where `graph` is a DAG of trigger -> script -> check -> notify nodes):

```json
{
  "name": "High disk usage",
  "description": "Free space when a disk exceeds 90%, notify if it stays full",
  "graph": {
    "nodes": [
      {"id": "t", "kind": "trigger", "name": "disk > 90%", "metric": "disk.used_percent", "op": ">", "threshold": 90, "next": "free"},
      {"id": "free", "kind": "script", "name": "free space", "lang": "sh", "script": "df -h", "timeout_s": 120, "next": "still"},
      {"id": "still", "kind": "check", "name": "if still > 90%", "metric": "disk.used_percent", "op": ">", "threshold": 90, "then": "notify", "else": ""},
      {"id": "notify", "kind": "notify", "name": "notify", "message": "disk still full after cleanup"}
    ]
  },
  "cooldown_seconds": 3600,
  "enabled": true
}
```

---

## Ticketing & Notifications

### Creating Tickets

```sh
# Title is required; queue defaults to "general", priority to "medium"
# (low | medium | high | critical)
curl -X POST \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"title":"Printer offline","description":"HP M404 on floor 3 is offline","queue":"general","priority":"high","device_id":"<device-id>"}' \
  https://rmm.example.com/api/tickets
```

### Notification Channels

Configure notification channels in Settings:

- **Email:** Uses the SMTP outbox
- **Slack:** Requires a webhook URL
- **Teams:** Requires a webhook URL
- **PagerDuty:** Requires service API key

### Notification Policies

Policies route alerts to specific channels based on severity, client, or
metric type. Configure via the UI.

---

## Reporting & Compliance

### Generating Reports

```sh
# On-demand generation. report_type: fleet_status | device |
# patch_compliance | license_compliance | uptime_sla; output_format:
# csv (default) or pdf. device_id is required for device reports.
curl -X POST \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"report_type":"fleet_status","output_format":"pdf"}' \
  https://rmm.example.com/api/reports/generate

# Device report
curl -X POST \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"report_type":"device","output_format":"csv","device_id":"<device-id>"}' \
  https://rmm.example.com/api/reports/generate

# Past runs
curl -s -H "Authorization: Bearer <token>" \
  https://rmm.example.com/api/reports/runs | jq '.'
```

### Scheduled Reports

Configure report schedules in the Reports UI, or via the API (schedule is
an ISO 8601 interval, e.g. `24h`):

```sh
curl -X POST \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"name":"Daily fleet report","report_type":"fleet_status","schedule":"24h","output_format":"csv"}' \
  https://rmm.example.com/api/reports/schedules
```

### Available Report Types

| Report | Description |
| ------ | ----------- |
| Fleet Status | Overview of all devices, online/offline status |
| Device Report | Detailed metrics and history for a single device |
| Patch Compliance | Installed vs. available patches by device/client |
| License Compliance | Installed software versions and license tracking |
| Uptime/SLA | Per-client uptime and SLA compliance |

---

## Patch Management

### Querying Available Patches

```sh
# Query Windows Update for available patches. The optional severity
# filter rides the "path" field (substring match, e.g. "critical").
curl -X POST \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"action":"patch_query","path":"critical"}' \
  https://rmm.example.com/api/devices/<device-id>/commands
```

### Installing Patches

```sh
# Approve and install specific patches. The patch ids travel as a
# base64-encoded JSON array in the "script" field:
#   echo -n '["KB123456"]' | base64   ->  WyJLQjEyMzQ1NiJd
# timeout_s > 0 also schedules a reboot after that many seconds.
# The UI performs this encoding automatically.
curl -X POST \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"action":"patch_apply","script":"WyJLQjEyMzQ1NiJd"}' \
  https://rmm.example.com/api/devices/<device-id>/commands
```

### Patch Policies

Configure patch policies in the UI:

- Auto-approve critical security patches
- Schedule reboots during maintenance windows
- Roll back patches on failure

---

## Monitoring & Maintenance

### Server Health

```sh
# Overall health check
curl -fsS https://rmm.example.com/healthz

# Individual service health
docker compose ps
```

### Log Management

```sh
# Server logs
docker compose logs server --tail 100

# Agent logs (via Loki)
curl -s "https://rmm.example.com/api/devices/<device-id>/events?level=error"
```

### Database Maintenance

```sh
# Vacuum TimescaleDB
docker compose exec timescale psql -U ourway-rmm -d ourway-rmm -c "VACUUM ANALYZE;"

# Retention policy (delete metrics older than 90 days)
docker compose exec timescale psql -U ourway-rmm -d ourway-rmm -c \
  "SELECT add_retention_policy('metrics', INTERVAL '90 days');"
```

---

## Backup & Disaster Recovery

### Database Backup

```sh
# Backup TimescaleDB
docker compose --env-file .env.prod -f docker-compose.prod.yml exec timescale \
  pg_dump -U ourway-rmm ourway-rmm > ourway-rmm-backup.sql

# Restore
cat ourway-rmm-backup.sql | \
  docker compose --env-file .env.prod -f docker-compose.prod.yml exec -T timescale psql -U ourway-rmm ourway-rmm
```

### File Storage Backup

```sh
# MinIO publishes no ports on the prod stack - run mc inside the
# container. Credentials come from .env.prod: OURWAY_RMM_MINIO_USER
# (default ourway-rmm) and OURWAY_RMM_MINIO_PASSWORD.
docker compose --env-file .env.prod -f docker-compose.prod.yml exec minio \
  mc alias set prod http://localhost:9000 "$OURWAY_RMM_MINIO_USER" "$OURWAY_RMM_MINIO_PASSWORD"
docker compose --env-file .env.prod -f docker-compose.prod.yml exec minio \
  mc cp --recursive "prod/<bucket>" ./minio-backup
```

### Complete Backup

```sh
# Backup all volumes. The prod compose project is named
# ourway-rmm-prod, so its volumes carry that prefix.
docker compose --env-file .env.prod -f docker-compose.prod.yml down
docker run --rm -v /var/lib/docker/volumes:/volumes -v "$PWD":/backup alpine \
  tar -czf /backup/ourway-rmm-full-backup.tar.gz -C /volumes \
  ourway-rmm-prod-timescale-data ourway-rmm-prod-nats-data \
  ourway-rmm-prod-redis-data ourway-rmm-prod-minio-data \
  ourway-rmm-prod-meili-data ourway-rmm-prod-loki-data
# (ourway-rmm-prod-caddy-data holds regeneratable TLS certs and is
# omitted.)
docker compose --env-file .env.prod --profile edge -f docker-compose.prod.yml up -d
```

---

## Security Hardening

### TLS Configuration

The bundled Caddy edge automatically obtains Let's Encrypt certificates.
Ensure `OURWAY_RMM_PUBLIC_URL` is set correctly for DNS challenge validation.

### Firewall Rules

Open only necessary ports:

| Port | Protocol | Purpose |
| ---- | -------- | ------- |
| 80 | TCP | HTTP (Let's Encrypt challenge) |
| 443 | TCP | HTTPS (operator UI/API) |
| 50052 | TCP | Agent mTLS gRPC |

All other ports should be internal only.

### Secret Rotation

Rotate secrets annually or after suspected exposure:

```sh
# Generate new JWT secret
openssl rand -hex 32

# Update .env.prod and restart
docker compose --env-file .env.prod --profile edge -f docker-compose.prod.yml up -d --build
```

### Regular Updates

```sh
# Update agent binaries
make agent && make sign

# Update the server (the prod image is built locally from source;
# there is no registry to pull from)
make prod
```

