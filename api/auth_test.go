package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticationLifecycle(t *testing.T) {
	server := newServer(NewAuthService([]byte("test-secret-that-is-long-enough")))

	response := request(t, server, http.MethodPost, "/auth/register", map[string]string{"email": "Ada@Example.com", "password": "correct horse battery staple"})
	if response.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", response.Code, http.StatusCreated)
	}

	response = request(t, server, http.MethodPost, "/auth/sign-in", map[string]string{"email": "ada@example.com", "password": "correct horse battery staple"})
	if response.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d, want %d", response.Code, http.StatusOK)
	}
	var signedIn tokensResponse
	if err := json.NewDecoder(response.Body).Decode(&signedIn); err != nil {
		t.Fatalf("decode sign-in response: %v", err)
	}
	if signedIn.AccessToken == "" || signedIn.RefreshToken == "" || signedIn.TokenType != "Bearer" {
		t.Fatalf("unexpected token response: %+v", signedIn)
	}

	response = request(t, server, http.MethodPost, "/auth/refresh", map[string]string{"refreshToken": signedIn.RefreshToken})
	if response.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want %d", response.Code, http.StatusOK)
	}
	var refreshed tokensResponse
	if err := json.NewDecoder(response.Body).Decode(&refreshed); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}

	response = request(t, server, http.MethodPost, "/auth/sign-out", map[string]string{"refreshToken": refreshed.RefreshToken})
	if response.Code != http.StatusNoContent {
		t.Fatalf("sign-out status = %d, want %d", response.Code, http.StatusNoContent)
	}

	response = request(t, server, http.MethodPost, "/auth/refresh", map[string]string{"refreshToken": refreshed.RefreshToken})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked refresh status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func request(t *testing.T, server http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}
