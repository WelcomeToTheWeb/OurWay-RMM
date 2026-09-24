-- 0018_notification_channels.sql — gap #6: notification channel persistence.
-- Channels are persisted here instead of the in-memory store so they survive
-- server restarts.

CREATE TABLE IF NOT EXISTS notification_channels (
    id         text PRIMARY KEY,              -- 'ch-' + 12 hex
    type       text NOT NULL
               CHECK (type IN ('email', 'slack', 'webhook', 'pagerduty', 'teams')),
    name       text NOT NULL,
    config     jsonb NOT NULL DEFAULT '{}',
    enabled    boolean NOT NULL DEFAULT true
);
