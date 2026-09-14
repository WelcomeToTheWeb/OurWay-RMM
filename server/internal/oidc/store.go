package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const oidcConfigKey = "oidc"

// Store manages OIDC provider configuration persistence.
type Store interface {
	// Get retrieves the stored OIDC configuration.
	Get(ctx context.Context) (*ProviderConfig, error)
	// Save persists the OIDC configuration.
	Save(ctx context.Context, cfg *ProviderConfig) error
}

// MemoryStore is an in-memory implementation for testing.
type MemoryStore struct {
	cfg *ProviderConfig
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (m *MemoryStore) Get(ctx context.Context) (*ProviderConfig, error) {
	if m.cfg == nil {
		return &ProviderConfig{}, nil
	}
	return m.cfg, nil
}

func (m *MemoryStore) Save(ctx context.Context, cfg *ProviderConfig) error {
	m.cfg = cfg
	return nil
}

// PostgresStore persists OIDC config in the server_config table.
type PostgresStore struct {
	db *pgxpool.Pool
}

func NewPostgresStore(db *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) Get(ctx context.Context) (*ProviderConfig, error) {
	var raw string
	err := p.db.QueryRow(ctx, `SELECT value FROM server_config WHERE key = $1`, oidcConfigKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return &ProviderConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("oidc get config: %w", err)
	}

	var cfg ProviderConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("oidc parse config: %w", err)
	}
	return &cfg, nil
}

func (p *PostgresStore) Save(ctx context.Context, cfg *ProviderConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("oidc marshal config: %w", err)
	}

	_, err = p.db.Exec(ctx, `
		INSERT INTO server_config (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, updated_at = now()
	`, oidcConfigKey, raw)
	if err != nil {
		return fmt.Errorf("oidc save config: %w", err)
	}
	return nil
}
