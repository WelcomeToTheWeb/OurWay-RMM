// Package oidc implements OpenID Connect (OIDC) authentication.
//
// OIDC is configured through admin settings (issuer URL, client ID, client
// secret). When enabled, operators can sign in with their OIDC identity
// provider instead of (or in addition to) the built-in username/password.
//
// The flow is:
//  1. POST /api/oidc/login -> redirect to OIDC provider
//  2. User authenticates at provider
//  3. Provider redirects back to /api/oidc/callback?code=...&state=...
//  4. Server exchanges code for tokens, validates ID token, issues RMMWay session JWT
//
// Users are auto-provisioned on first OIDC login.
package oidc

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// ProviderConfig holds the OIDC provider settings stored by the admin.
type ProviderConfig struct {
	Enabled      bool     `json:"enabled"`
	IssuerURL    string   `json:"issuer_url"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Scopes       []string `json:"scopes"`      // defaults to ["openid", "email", "profile"]
	RoleClaim    string   `json:"role_claim"`  // claim to map to role (optional)
	EmailClaim   string   `json:"email_claim"` // claim to use for email (default: email)
}

// DefaultScopes for OIDC login.
var DefaultScopes = []string{"openid", "email", "profile"}

// StateStore holds pending OIDC auth states (state -> callback data).
type StateStore struct {
	mu     sync.RWMutex
	states map[string]*StateEntry
}

// StateEntry holds the pending OIDC auth state data.
type StateEntry struct {
	Nonce        string
	CodeVerifier string
	CallbackURL  string
	CreatedAt    time.Time
}

// NewStateStore creates a new state store.
func NewStateStore() *StateStore {
	return &StateStore{states: make(map[string]*StateEntry)}
}

// Store saves a new OIDC auth state. Returns the state string to include in the auth URL.
func (s *StateStore) Store(nonce, codeVerifier, callbackURL string) (string, error) {
	state := randomString(32)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state] = &StateEntry{
		Nonce: nonce, CodeVerifier: codeVerifier, CallbackURL: callbackURL,
		CreatedAt: time.Now(),
	}
	return state, nil
}

// Get retrieves and removes a pending OIDC state.
func (s *StateStore) Get(state string) (*StateEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.states[state]
	if ok {
		delete(s.states, state)
	}
	return entry, ok
}

// Purge removes expired states (older than 10 minutes).
func (s *StateStore) Purge() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	for k, v := range s.states {
		if v.CreatedAt.Before(cutoff) {
			delete(s.states, k)
		}
	}
}

// GeneratePKCE generates a PKCE code verifier and challenge.
func GeneratePKCE() (verifier, challenge string) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		panic("oidc: rand failed: " + err.Error())
	}
	verifier = base64.RawURLEncoding.EncodeToString(verifierBytes)

	challengeHash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(challengeHash[:])
	return verifier, challenge
}

func randomString(n int) string {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		panic("oidc: rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// OAuth2Config builds the oauth2.Config from the provider config.
func OAuth2Config(cfg ProviderConfig, redirectURL string) *oauth2.Config {
	if cfg.ClientSecret == "" {
		// Public client (PKCE only)
		return &oauth2.Config{
			ClientID: cfg.ClientID,
			Endpoint: oauth2.Endpoint{
				AuthURL:  cfg.IssuerURL + "/authorize",
				TokenURL: cfg.IssuerURL + "/token",
			},
			RedirectURL: redirectURL,
			Scopes:      scopes(cfg.Scopes),
		}
	}
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.IssuerURL + "/authorize",
			TokenURL: cfg.IssuerURL + "/token",
		},
		RedirectURL: redirectURL,
		Scopes:      scopes(cfg.Scopes),
	}
}

func scopes(scopes []string) []string {
	if len(scopes) == 0 {
		return DefaultScopes
	}
	// Ensure openid is included
	for _, s := range scopes {
		if s == "openid" {
			return scopes
		}
	}
	return append([]string{"openid"}, scopes...)
}

// IDTokenClaims holds the claims extracted from an OIDC ID token.
type IDTokenClaims struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
	Role          string `json:"role"`
}

// ParseIDTokenClaims parses raw claims into our struct.
func ParseIDTokenClaims(claims map[string]interface{}) (*IDTokenClaims, error) {
	result := &IDTokenClaims{}

	if sub, ok := claims["sub"].(string); ok {
		result.Sub = sub
	} else {
		return nil, errors.New("oidc: missing or invalid 'sub' claim")
	}

	if email, ok := claims["email"].(string); ok {
		result.Email = email
	}
	if verified, ok := claims["email_verified"].(bool); ok {
		result.EmailVerified = verified
	}
	if name, ok := claims["name"].(string); ok {
		result.Name = name
	}
	if picture, ok := claims["picture"].(string); ok {
		result.Picture = picture
	}
	if role, ok := claims["role"].(string); ok {
		result.Role = role
	}

	return result, nil
}

// OIDCIdentity represents a user identified via OIDC.
type OIDCIdentity struct {
	ProviderSub string // OIDC subject
	Username    string // Local username
	Email       string // Email address
	Name        string // Display name
	Role        string // RBAC role (admin/tech/viewer)
	Picture     string // Avatar URL
}

// UsernameFromEmail generates a local username from an email address.
func UsernameFromEmail(email string) string {
	if email == "" {
		return "user"
	}
	parts := strings.Split(email, "@")
	return strings.ToLower(parts[0])
}

// MapClaimsToIdentity converts ID token claims to an OIDC identity.
func MapClaimsToIdentity(claims *IDTokenClaims, rawClaims map[string]interface{}, roleClaim string) *OIDCIdentity {
	identity := &OIDCIdentity{
		ProviderSub: claims.Sub,
		Email:       claims.Email,
		Name:        claims.Name,
		Username:    UsernameFromEmail(claims.Email),
		Picture:     claims.Picture,
	}

	// Determine role from claims
	if roleClaim != "" {
		if role, ok := rawClaims[roleClaim].(string); ok {
			identity.Role = normalizeRole(role)
		}
	}

	// Default role if not mapped
	if identity.Role == "" {
		identity.Role = "viewer"
	}

	return identity
}

func normalizeRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "admin", "administrator", "owner":
		return "admin"
	case "tech", "technician", "agent":
		return "tech"
	default:
		return "viewer"
	}
}
