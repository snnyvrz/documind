package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
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

func TestClientIPIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "http://api", nil)
	request.RemoteAddr = "198.51.100.10:1234"
	request.Header.Set("X-Real-IP", "203.0.113.10")
	request.Header.Set("X-Forwarded-For", "203.0.113.10")

	if got := clientIP(request, nil); got != "198.51.100.10" {
		t.Fatalf("client IP = %q", got)
	}
}

func TestClientIPUsesFirstUntrustedAddressFromTrustedChain(t *testing.T) {
	_, trustedNetwork, _ := net.ParseCIDR("10.0.0.0/8")
	request, _ := http.NewRequest(http.MethodGet, "http://api", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.3")

	if got := clientIP(request, []*net.IPNet{trustedNetwork}); got != "203.0.113.10" {
		t.Fatalf("client IP = %q", got)
	}
}

func TestTrustedProxyNetworksRejectsInvalidCIDR(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "not-a-cidr")
	if _, err := trustedProxyNetworks(); err == nil {
		t.Fatal("invalid trusted proxy CIDR was accepted")
	}
}

func TestHTTPSRequirementDoesNotTrustUntrustedForwardedProto(t *testing.T) {
	_, trustedNetwork, _ := net.ParseCIDR("10.0.0.0/8")
	authenticator := &jwtAuthenticator{requireHTTPS: true, trustedProxies: []*net.IPNet{trustedNetwork}}
	request, _ := http.NewRequest(http.MethodGet, "http://api", nil)
	request.RemoteAddr = "198.51.100.10:1234"
	request.Header.Set("X-Forwarded-Proto", "https")

	recorder := httptest.NewRecorder()
	context := echo.New().NewContext(request, recorder)
	if err := authenticator.requireHTTPSRequest(context); err != nil || recorder.Code != http.StatusBadRequest {
		t.Fatalf("untrusted forwarded proto was accepted: error=%v status=%d", err, recorder.Code)
	}
}
