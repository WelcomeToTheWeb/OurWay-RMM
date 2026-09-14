package oidc

import (
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge := GeneratePKCE()

	if len(verifier) < 30 {
		t.Errorf("code verifier too short: %d", len(verifier))
	}
	if len(challenge) < 30 {
		t.Errorf("code challenge too short: %d", len(challenge))
	}
}

func TestUsernameFromEmail(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{"test@example.com", "test"},
		{"Test.Name@example.com", "test.name"},
		{"user+tag@example.com", "user+tag"},
		{"", "user"},
	}

	for _, tt := range tests {
		result := UsernameFromEmail(tt.email)
		if result != tt.expected {
			t.Errorf("UsernameFromEmail(%q) = %q; want %q", tt.email, result, tt.expected)
		}
	}
}

func TestNormalizeRole(t *testing.T) {
	tests := []struct {
		role     string
		expected string
	}{
		{"admin", "admin"},
		{"Admin", "admin"},
		{"administrator", "admin"},
		{"owner", "admin"},
		{"tech", "tech"},
		{"technician", "tech"},
		{"agent", "tech"},
		{"viewer", "viewer"},
		{"user", "viewer"},
		{"", "viewer"},
	}

	for _, tt := range tests {
		result := normalizeRole(tt.role)
		if result != tt.expected {
			t.Errorf("normalizeRole(%q) = %q; want %q", tt.role, result, tt.expected)
		}
	}
}

func TestParseIDTokenClaims(t *testing.T) {
	claims := map[string]interface{}{
		"sub":            "12345",
		"email":          "test@example.com",
		"email_verified": true,
		"name":           "Test User",
		"picture":        "https://example.com/pic.jpg",
	}

	result, err := ParseIDTokenClaims(claims)
	if err != nil {
		t.Fatalf("ParseIDTokenClaims failed: %v", err)
	}

	if result.Sub != "12345" {
		t.Errorf("Sub = %q; want 12345", result.Sub)
	}
	if result.Email != "test@example.com" {
		t.Errorf("Email = %q; want test@example.com", result.Email)
	}
	if !result.EmailVerified {
		t.Errorf("EmailVerified = false; want true")
	}
	if result.Name != "Test User" {
		t.Errorf("Name = %q; want Test User", result.Name)
	}
}

func TestMapClaimsToIdentity(t *testing.T) {
	claims := &IDTokenClaims{
		Sub:       "12345",
		Email:     "test@example.com",
		Name:      "Test User",
		Picture:   "https://example.com/pic.jpg",
	}

	identity := MapClaimsToIdentity(claims, map[string]interface{}{}, "")

	if identity.ProviderSub != "12345" {
		t.Errorf("ProviderSub = %q; want 12345", identity.ProviderSub)
	}
	if identity.Username != "test" {
		t.Errorf("Username = %q; want test", identity.Username)
	}
	if identity.Email != "test@example.com" {
		t.Errorf("Email = %q; want test@example.com", identity.Email)
	}
	if identity.Name != "Test User" {
		t.Errorf("Name = %q; want Test User", identity.Name)
	}
	// Default role should be viewer
	if identity.Role != "viewer" {
		t.Errorf("Role = %q; want viewer", identity.Role)
	}
}

func TestStateStore(t *testing.T) {
	store := NewStateStore()

	state, err := store.Store("nonce123", "verifier456", "/callback")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if len(state) < 10 {
		t.Errorf("state too short: %d", len(state))
	}

	entry, ok := store.Get(state)
	if !ok {
		t.Fatal("Get failed to find stored state")
	}
	if entry.Nonce != "nonce123" {
		t.Errorf("Nonce = %q; want nonce123", entry.Nonce)
	}
	if entry.CodeVerifier != "verifier456" {
		t.Errorf("CodeVerifier = %q; want verifier456", entry.CodeVerifier)
	}
	if entry.CallbackURL != "/callback" {
		t.Errorf("CallbackURL = %q; want /callback", entry.CallbackURL)
	}

	// Get should remove the state
	_, ok = store.Get(state)
	if ok {
		t.Error("Get should have removed the state after retrieval")
	}
}

func TestRandomString(t *testing.T) {
	s := randomString(16)
	if len(s) != 22 {
		// base64 RawURLEncoding of 16 bytes is 22 characters
		t.Errorf("randomString(16) length = %d; want 22", len(s))
	}
}

func TestScopes(t *testing.T) {
	// Default scopes
	s := scopes([]string{})
	if len(s) != 3 {
		t.Errorf("default scopes length = %d; want 3", len(s))
	}
	if s[0] != "openid" {
		t.Errorf("first scope = %q; want openid", s[0])
	}

	// Custom scopes with openid
	s = scopes([]string{"openid", "email", "profile"})
	if len(s) != 3 {
		t.Errorf("custom scopes length = %d; want 3", len(s))
	}

	// Custom scopes without openid (should be prepended)
	s = scopes([]string{"email", "profile"})
	if s[0] != "openid" {
		t.Errorf("first scope = %q; want openid (prepended)", s[0])
	}
}
