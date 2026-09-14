// C #10b: OpenID Connect (OIDC) authentication.
//
// Routes:
//
//	POST /api/oidc/login -> redirect to OIDC provider authorization endpoint
//	GET  /api/oidc/callback?code=...&state=... -> exchange code, issue JWT
//	GET  /api/oidc/status -> check if OIDC is configured/enabled
//	POST /api/oidc/config -> save OIDC provider configuration (admin)
//	GET  /api/oidc/config -> retrieve OIDC provider configuration (admin)
//
// OIDC is an alternative authentication method that allows users to log in
// with their identity provider (e.g., Google, GitHub, Azure AD, Keycloak).
package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	oidcProvider "github.com/coreos/go-oidc/v3/oidc"
	"github.com/welcometotheweb/rmmway/server/internal/oidc"
	"github.com/welcometotheweb/rmmway/server/internal/store"
	"github.com/welcometotheweb/rmmway/server/internal/users"
	"golang.org/x/oauth2"
)

// registerOIDC mounts the OIDC authentication routes.
func registerOIDC(s *Server, mux *http.ServeMux) {
	mux.HandleFunc("/api/oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("/api/oidc/callback", s.handleOIDCCallback)
	mux.HandleFunc("/api/oidc/status", s.handleOIDCStatus)

	// Config routes are admin-only
	config := s.rbacRoleGate(s.handleOIDCConfig, "admin")
	mux.HandleFunc("/api/oidc/config", config)
	mux.HandleFunc("/admin/oidc/config", config)
}

// handleOIDCLogin initiates the OIDC authorization flow.
// POST body: {callback_url?: string} (optional, for SPA apps)
// Returns: redirect URL to the OIDC provider
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	// Get OIDC configuration
	cfg, err := s.oidcStore.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to load OIDC config: %v", err),
		})
		return
	}
	if !cfg.Enabled {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "OIDC authentication is not enabled",
		})
		return
	}
	if cfg.IssuerURL == "" || cfg.ClientID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "OIDC provider is not configured",
		})
		return
	}

	// Parse optional callback URL
	var body struct {
		CallbackURL string `json:"callback_url"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	callbackURL := body.CallbackURL
	if callbackURL == "" {
		// Default to login page
		callbackURL = "/"
	}

	// Validate callback URL is same origin (prevent open redirect)
	if !strings.HasPrefix(callbackURL, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "callback_url must be a relative path",
		})
		return
	}

	// Generate PKCE code verifier and challenge
	verifier, challenge := oidc.GeneratePKCE()

	// Generate nonce
	nonceBytes := make([]byte, 16)
	rand.Read(nonceBytes)
	nonce := fmt.Sprintf("%x", nonceBytes)

	// Store state
	state, err := s.oidcStateStore.Store(nonce, verifier, callbackURL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to store auth state: %v", err),
		})
		return
	}

	// Build OAuth2 config
	oauthConfig := oidc.OAuth2Config(*cfg, s.oidcCallbackURL())

	// Build authorization URL
	authURL := oauthConfig.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("nonce", nonce),
	)

	writeJSON(w, http.StatusOK, map[string]string{
		"auth_url": authURL,
	})
}

// handleOIDCCallback processes the OIDC provider's callback after user login.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	// Get OIDC configuration
	cfg, err := s.oidcStore.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to load OIDC config: %v", err),
		})
		return
	}

	// Extract authorization code and state from query params
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		// Redirect to login page with error
		http.Redirect(w, r, "/?oidc_error=callback_parameters_missing", http.StatusFound)
		return
	}

	// Retrieve the stored state
	entry, ok := s.oidcStateStore.Get(state)
	if !ok {
		http.Redirect(w, r, "/?oidc_error=invalid_state", http.StatusFound)
		return
	}

	// Exchange code for tokens
	oauthConfig := oidc.OAuth2Config(*cfg, s.oidcCallbackURL())
	token, err := oauthConfig.Exchange(r.Context(), code,
		oauth2.SetAuthURLParam("code_verifier", entry.CodeVerifier))
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/?oidc_error=code_exchange_failed&details=%s",
			url.QueryEscape(err.Error())), http.StatusFound)
		return
	}

	// Parse and validate ID token
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		http.Redirect(w, r, "/?oidc_error=no_id_token", http.StatusFound)
		return
	}

	// Validate token against issuer
	provider, err := oidcProvider.NewProvider(r.Context(), cfg.IssuerURL)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/?oidc_error=provider_error&details=%s",
			url.QueryEscape(err.Error())), http.StatusFound)
		return
	}

	verifier := provider.Verifier(&oidcProvider.Config{ClientID: cfg.ClientID})
	idToken, err := verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/?oidc_error=id_token_invalid&details=%s",
			url.QueryEscape(err.Error())), http.StatusFound)
		return
	}

	// Check nonce
	var claims map[string]interface{}
	if err := idToken.Claims(&claims); err != nil {
		http.Redirect(w, r, "/?oidc_error=claims_parse_failed", http.StatusFound)
		return
	}
	if nonce, ok := claims["nonce"].(string); !ok || nonce != entry.Nonce {
		http.Redirect(w, r, "/?oidc_error=nonce_mismatch", http.StatusFound)
		return
	}

	// Extract user info
	oidcClaims, err := oidc.ParseIDTokenClaims(claims)
	if err != nil {
		http.Redirect(w, r, "/?oidc_error=claims_invalid", http.StatusFound)
		return
	}

	// Map to OIDC identity
	identity := oidc.MapClaimsToIdentity(oidcClaims, claims, cfg.RoleClaim)

	// Look up or create local user
	var user *store.User
	if identity.Email != "" {
		// Try to find existing user by email
		existingUsers, err := s.users.List(r.Context())
		if err == nil {
			for _, u := range existingUsers {
				if strings.EqualFold(u.Username, identity.Username) {
					user = u
					break
				}
			}
		}
	}

	if user == nil {
		// Auto-provision: create a new local user
		password := generateRandomPassword(20)
		user, err = s.users.Create(r.Context(), identity.Username, identity.Role, password, []string{})
		if err != nil && !errors.Is(err, store.ErrUsernameExists) {
			http.Redirect(w, r, fmt.Sprintf("/?oidc_error=user_create_failed&details=%s",
				url.QueryEscape(err.Error())), http.StatusFound)
			return
		}
		if user == nil {
			// Username already exists, try to find it
			existingUsers, err := s.users.List(r.Context())
			if err == nil {
				for _, u := range existingUsers {
					if strings.EqualFold(u.Username, identity.Username) {
						user = u
						break
					}
				}
			}
		}
	}

	if user == nil {
		http.Redirect(w, r, "/?oidc_error=user_not_found", http.StatusFound)
		return
	}

	// Issue RMMWay session JWT
	tok, err := users.MintSessionJWT(s.jwtSecret, s.tokenLifetime, users.SessionClaims{
		Role:     user.Role,
		Username: user.Username,
		Caps:     s.adminCaps,
	})
	if err != nil {
		http.Redirect(w, r, "/?oidc_error=jwt_mint_failed", http.StatusFound)
		return
	}

	// Record login
	_ = s.users.MarkLoggedIn(r.Context(), user.ID)

	// Redirect to callback URL with token in query string
	redirectURL := entry.CallbackURL + "?oidc_token=" + url.QueryEscape(tok)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// handleOIDCStatus reports whether OIDC authentication is available.
func (s *Server) handleOIDCStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.oidcStore.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to load OIDC config: %v", err),
		})
		return
	}

	// Don't expose sensitive info
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    cfg.Enabled,
		"configured": cfg.IssuerURL != "" && cfg.ClientID != "",
		"provider":   cfg.IssuerURL,
	})
}

// handleOIDCConfig manages the OIDC provider configuration.
// GET: retrieve configuration (admin)
// POST: save configuration (admin)
func (s *Server) handleOIDCConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := s.oidcStore.Get(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to load OIDC config: %v", err),
			})
			return
		}
		writeJSON(w, http.StatusOK, cfg)

	case http.MethodPost:
		var cfg oidc.ProviderConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Validate configuration
		if cfg.Enabled {
			if cfg.IssuerURL == "" || cfg.ClientID == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "issuer_url and client_id are required when OIDC is enabled",
				})
				return
			}
			// Validate issuer URL
			if _, err := url.Parse(cfg.IssuerURL); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "issuer_url is not a valid URL",
				})
				return
			}
			// Ensure issuer URL has trailing slash for URL concatenation
			if !strings.HasSuffix(cfg.IssuerURL, "/") {
				cfg.IssuerURL += "/"
			}
		}

		if err := s.oidcStore.Save(r.Context(), &cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to save OIDC config: %v", err),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// oidcCallbackURL returns the full callback URL for the OIDC flow.
func (s *Server) oidcCallbackURL() string {
	publicURL := s.publicURL
	if publicURL == "" {
		publicURL = "http://localhost:8080"
	}
	// Strip trailing slash if present
	publicURL = strings.TrimSuffix(publicURL, "/")
	return publicURL + "/api/oidc/callback"
}

// generateRandomPassword generates a random password for auto-provisioned users.
func generateRandomPassword(length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	password := make([]byte, length)
	for i := range password {
		n := make([]byte, 1)
		rand.Read(n)
		password[i] = chars[n[0]%byte(len(chars))]
	}
	return string(password)
}
