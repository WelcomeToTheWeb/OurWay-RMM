package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/welcometotheweb/ourway-rmm/server/internal/notify"
)

// PostgresNotifyStore is a Postgres-backed store for notification channels
// and policies.
type PostgresNotifyStore struct {
	db *pgxpool.Pool
}

// NewPostgresNotifyStore creates a Postgres-backed notification store.
func NewPostgresNotifyStore(db *pgxpool.Pool) *PostgresNotifyStore {
	return &PostgresNotifyStore{db: db}
}

func (s *PostgresNotifyStore) ListChannels(ctx context.Context) ([]*notify.ChannelConfig, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, type, name, config, enabled
		FROM notification_channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*notify.ChannelConfig
	for rows.Next() {
		var ch notify.ChannelConfig
		var cfg json.RawMessage
		if err := rows.Scan(&ch.ID, &ch.Type, &ch.Name, &cfg, &ch.Enabled); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(cfg, &ch.Config)
		out = append(out, &ch)
	}
	return out, rows.Err()
}

// GetChannel returns a single channel by ID.
func (s *PostgresNotifyStore) GetChannel(ctx context.Context, id string) (*notify.ChannelConfig, error) {
	var ch notify.ChannelConfig
	var cfg json.RawMessage
	err := s.db.QueryRow(ctx, `
		SELECT id, type, name, config, enabled
		FROM notification_channels WHERE id = $1`, id).Scan(
		&ch.ID, &ch.Type, &ch.Name, &cfg, &ch.Enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("channel %s not found", id)
		}
		return nil, err
	}
	_ = json.Unmarshal(cfg, &ch.Config)
	return &ch, nil
}

// CreateChannel creates a new notification channel.
func (s *PostgresNotifyStore) CreateChannel(ctx context.Context, ch *notify.ChannelConfig) (*notify.ChannelConfig, error) {
	id, err := newChannelID()
	if err != nil {
		return nil, err
	}
	cfgBytes, err := json.Marshal(ch.Config)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO notification_channels (id, type, name, config, enabled)
		VALUES ($1, $2, $3, $4, $5)`,
		id, ch.Type, ch.Name, cfgBytes, ch.Enabled)
	if err != nil {
		return nil, err
	}
	ch.ID = id
	return ch, nil
}

// UpdateChannel updates an existing notification channel.
func (s *PostgresNotifyStore) UpdateChannel(ctx context.Context, ch *notify.ChannelConfig) (*notify.ChannelConfig, error) {
	cfgBytes, err := json.Marshal(ch.Config)
	if err != nil {
		return nil, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE notification_channels
		SET type = $1, name = $2, config = $3, enabled = $4
		WHERE id = $5`,
		ch.Type, ch.Name, cfgBytes, ch.Enabled, ch.ID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("channel %s not found", ch.ID)
	}
	return ch, nil
}

// DeleteChannel deletes a notification channel.
func (s *PostgresNotifyStore) DeleteChannel(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM notification_channels WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("channel %s not found", id)
	}
	return nil
}

// ListPolicies returns all notification policies.
func (s *PostgresNotifyStore) ListPolicies(ctx context.Context) ([]*NotificationPolicy, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, category, client_id, role, channels, enabled, created_at, updated_at
		FROM notification_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NotificationPolicy
	for rows.Next() {
		var p NotificationPolicy
		if err := rows.Scan(&p.ID, &p.Category, &p.ClientID, &p.Role, &p.Channels, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// GetPolicy returns a single policy by ID.
func (s *PostgresNotifyStore) GetPolicy(ctx context.Context, id string) (*NotificationPolicy, error) {
	var p NotificationPolicy
	err := s.db.QueryRow(ctx, `
		SELECT id, category, client_id, role, channels, enabled, created_at, updated_at
		FROM notification_policies WHERE id = $1`, id).Scan(
		&p.ID, &p.Category, &p.ClientID, &p.Role, &p.Channels, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("policy %s not found", id)
		}
		return nil, err
	}
	return &p, nil
}

// CreatePolicy creates a new notification policy.
func (s *PostgresNotifyStore) CreatePolicy(ctx context.Context, p *NotificationPolicy) (*NotificationPolicy, error) {
	id, err := newPolicyID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	_, err = s.db.Exec(ctx, `
		INSERT INTO notification_policies (id, category, client_id, role, channels, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		id, p.Category, p.ClientID, p.Role, p.Channels, p.Enabled, now)
	if err != nil {
		return nil, err
	}
	p.ID = id
	p.CreatedAt = now
	p.UpdatedAt = now
	return p, nil
}

// UpdatePolicy updates an existing notification policy.
func (s *PostgresNotifyStore) UpdatePolicy(ctx context.Context, id string, p *NotificationPolicy) (*NotificationPolicy, error) {
	now := time.Now().UTC()
	tag, err := s.db.Exec(ctx, `
		UPDATE notification_policies
		SET category = $1, client_id = $2, role = $3, channels = $4, enabled = $5, updated_at = $6
		WHERE id = $7`,
		p.Category, p.ClientID, p.Role, p.Channels, p.Enabled, now, p.ID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("policy %s not found", p.ID)
	}
	p.UpdatedAt = now
	return p, nil
}

// DeletePolicy deletes a notification policy.
func (s *PostgresNotifyStore) DeletePolicy(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM notification_policies WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("policy %s not found", id)
	}
	return nil
}
