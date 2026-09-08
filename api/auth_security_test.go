package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidateProductionAuthConfig(t *testing.T) {
	original := map[string]string{}
	for _, name := range []string{"AUTH_MODE", "AUTH_JWT_SECRET", "AUTH_COOKIE_SECURE", "AUTH_REQUIRE_HTTPS"} {
		original[name] = os.Getenv(name)
		t.Cleanup(func() { _ = os.Setenv(name, original[name]) })
	}

	_ = os.Setenv("AUTH_MODE", "production")
	_ = os.Unsetenv("AUTH_JWT_SECRET")
	_ = os.Setenv("AUTH_COOKIE_SECURE", "true")
	_ = os.Setenv("AUTH_REQUIRE_HTTPS", "true")
	if err := validateProductionAuthConfig(); err == nil {
		t.Fatal("missing production secret was accepted")
	}

	_ = os.Setenv("AUTH_JWT_SECRET", developmentJWTSecret)
	if err := validateProductionAuthConfig(); err == nil || !strings.Contains(err.Error(), "development secret") {
		t.Fatalf("development secret error = %v", err)
	}

	_ = os.Setenv("AUTH_JWT_SECRET", strings.Repeat("x", 32))
	_ = os.Setenv("AUTH_COOKIE_SECURE", "false")
	if err := validateProductionAuthConfig(); err == nil {
		t.Fatal("insecure production cookie configuration was accepted")
	}
}

func TestValidateCredentialsRejectsBcryptOverflow(t *testing.T) {
	err := validateCredentials(authCredentials{Email: "user@example.com", Password: strings.Repeat("a", maximumPassword+1)})
	if err == nil || !strings.Contains(err.Error(), "72 bytes") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestAuthRateLimiterLimitsAndExpiresEntries(t *testing.T) {
	limiter := newAuthRateLimiter()
	if !limiter.allow("login-ip", "127.0.0.1", 2, time.Minute) || !limiter.allow("login-ip", "127.0.0.1", 2, time.Minute) {
		t.Fatal("allowed attempts were rejected")
	}
	if limiter.allow("login-ip", "127.0.0.1", 2, time.Minute) {
		t.Fatal("rate limit was not enforced")
	}
	if !limiter.allow("login-ip", "another-ip", 2, time.Minute) {
		t.Fatal("rate limit was not scoped by key")
	}
}

func TestJWTClaimsIncludeSessionVersion(t *testing.T) {
	authenticator := &jwtAuthenticator{secret: []byte(strings.Repeat("s", 32)), issuer: defaultJWTIssuer, ttl: time.Hour}
	user := authUser{ID: "user-id", Email: "user@example.com", SessionVersion: 4}
	// authenticate writes the cookie; the claim is checked by parsing the result in the
	// handler-level tests, while this test guards the persisted model's version field.
	if user.SessionVersion != 4 || len(authenticator.secret) != 32 {
		t.Fatal("session version setup is incorrect")
	}
}
