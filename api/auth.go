package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"crypto/pbkdf2"

	"github.com/labstack/echo/v5"
)

const (
	accessTokenLifetime  = 15 * time.Minute
	refreshTokenLifetime = 7 * 24 * time.Hour
	passwordSaltSize     = 16
	passwordKeyLength    = 32
	passwordIterations   = 600_000
)

var (
	errInvalidCredentials = errors.New("invalid email or password")
	errInvalidToken       = errors.New("invalid or expired token")
	errEmailTaken         = errors.New("email is already registered")
)

type user struct {
	ID           string
	Email        string
	PasswordSalt []byte
	PasswordHash []byte
}

type refreshSession struct {
	UserID    string
	ExpiresAt time.Time
	Revoked   bool
}

type AuthService struct {
	secret   []byte
	mu       sync.RWMutex
	users    map[string]user
	sessions map[string]refreshSession
	now      func() time.Time
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type signInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type tokensResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	TokenType    string `json:"tokenType"`
	ExpiresIn    int64  `json:"expiresIn"`
}

type jwtClaims struct {
	Subject string `json:"sub"`
	TokenID string `json:"jti"`
	Type    string `json:"typ"`
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
}

func NewAuthService(secret []byte) *AuthService {
	return &AuthService{
		secret:   secret,
		users:    make(map[string]user),
		sessions: make(map[string]refreshSession),
		now:      time.Now,
	}
}

func (a *AuthService) Register(c *echo.Context) error {
	var request registerRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	user, err := a.register(request.Email, request.Password)
	if err != nil {
		return authError(c, err)
	}

	return c.JSON(http.StatusCreated, map[string]string{"id": user.ID, "email": user.Email})
}

func (a *AuthService) SignIn(c *echo.Context) error {
	var request signInRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	user, err := a.authenticate(request.Email, request.Password)
	if err != nil {
		return authError(c, err)
	}

	tokens, err := a.issueTokens(user.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not issue tokens"})
	}
	return c.JSON(http.StatusOK, tokens)
}

func (a *AuthService) Refresh(c *echo.Context) error {
	var request refreshRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	claims, err := a.parseToken(request.RefreshToken, "refresh")
	if err != nil || !a.consumeRefreshSession(claims.TokenID, claims.Subject) {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired refresh token"})
	}

	tokens, err := a.issueTokens(claims.Subject)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not issue tokens"})
	}
	return c.JSON(http.StatusOK, tokens)
}

func (a *AuthService) SignOut(c *echo.Context) error {
	var request refreshRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	claims, err := a.parseToken(request.RefreshToken, "refresh")
	if err != nil || !a.revokeRefreshSession(claims.TokenID, claims.Subject) {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired refresh token"})
	}
	return c.NoContent(http.StatusNoContent)
}

func (a *AuthService) register(email, password string) (user, error) {
	email = normalizeEmail(email)
	if email == "" || len(password) < 8 {
		return user{}, errors.New("email and a password of at least 8 characters are required")
	}

	salt := make([]byte, passwordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return user{}, err
	}
	passwordHash, err := hashPassword(password, salt)
	if err != nil {
		return user{}, err
	}
	created := user{ID: randomID(), Email: email, PasswordSalt: salt, PasswordHash: passwordHash}

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.users[email]; exists {
		return user{}, errEmailTaken
	}
	a.users[email] = created
	return created, nil
}

func (a *AuthService) authenticate(email, password string) (user, error) {
	a.mu.RLock()
	stored, exists := a.users[normalizeEmail(email)]
	a.mu.RUnlock()
	if !exists {
		return user{}, errInvalidCredentials
	}
	passwordHash, err := hashPassword(password, stored.PasswordSalt)
	if err != nil || subtle.ConstantTimeCompare(stored.PasswordHash, passwordHash) != 1 {
		return user{}, errInvalidCredentials
	}
	return stored, nil
}

func (a *AuthService) issueTokens(userID string) (tokensResponse, error) {
	now := a.now().UTC()
	accessToken, err := a.signToken(jwtClaims{Subject: userID, TokenID: randomID(), Type: "access", Issued: now.Unix(), Expires: now.Add(accessTokenLifetime).Unix()})
	if err != nil {
		return tokensResponse{}, err
	}
	refreshID := randomID()
	refreshExpiry := now.Add(refreshTokenLifetime)
	refreshToken, err := a.signToken(jwtClaims{Subject: userID, TokenID: refreshID, Type: "refresh", Issued: now.Unix(), Expires: refreshExpiry.Unix()})
	if err != nil {
		return tokensResponse{}, err
	}

	a.mu.Lock()
	a.sessions[refreshID] = refreshSession{UserID: userID, ExpiresAt: refreshExpiry}
	a.mu.Unlock()
	return tokensResponse{AccessToken: accessToken, RefreshToken: refreshToken, TokenType: "Bearer", ExpiresIn: int64(accessTokenLifetime.Seconds())}, nil
}

func (a *AuthService) consumeRefreshSession(tokenID, userID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, exists := a.sessions[tokenID]
	if !exists || session.Revoked || session.UserID != userID || !a.now().Before(session.ExpiresAt) {
		return false
	}
	session.Revoked = true
	a.sessions[tokenID] = session
	return true
}

func (a *AuthService) revokeRefreshSession(tokenID, userID string) bool {
	return a.consumeRefreshSession(tokenID, userID)
}

func (a *AuthService) signToken(claims jwtClaims) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (a *AuthService) parseToken(token, expectedType string) (jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, errInvalidToken
	}
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return jwtClaims{}, errInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, errInvalidToken
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Subject == "" || claims.TokenID == "" || claims.Type != expectedType || claims.Expires <= a.now().Unix() {
		return jwtClaims{}, errInvalidToken
	}
	return claims, nil
}

func authError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, errEmailTaken):
		return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, errInvalidCredentials):
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": err.Error()})
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

func hashPassword(password string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyLength)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func randomID() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic("could not generate secure random identifier")
	}
	return hex.EncodeToString(bytes)
}
