package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type principalKey struct{}

const (
	authCookie           = "documind_token"
	defaultJWTIssuer     = "documind"
	defaultJWTTTL        = 8 * time.Hour
	minimumPassword      = 8
	maximumPassword      = 72
	maximumAuthBody      = 8 << 10
	developmentJWTSecret = "local-development-secret-change-me-please"
)

type authUser struct {
	ID             string `gorm:"type:uuid;primaryKey"`
	Email          string `gorm:"size:320;uniqueIndex;not null"`
	PasswordHash   string `gorm:"not null"`
	SessionVersion uint   `gorm:"not null;default:0"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type jwtAuthenticator struct {
	secret       []byte
	issuer       string
	ttl          time.Duration
	secureCookie bool
	requireHTTPS bool
	database     *gorm.DB
	limiter      *authRateLimiter
}

type authCredentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type jwtClaims struct {
	Email          string `json:"email"`
	SessionVersion uint   `json:"sessionVersion"`
	jwt.RegisteredClaims
}

func newAuthenticator(database *gorm.DB) (*jwtAuthenticator, error) {
	secret := os.Getenv("AUTH_JWT_SECRET")
	if database == nil && secret == "" {
		return &jwtAuthenticator{limiter: newAuthRateLimiter()}, nil
	}
	if secret == "" {
		if authMode() == "development" && database == nil {
			return &jwtAuthenticator{limiter: newAuthRateLimiter()}, nil
		}
		return nil, errors.New("AUTH_JWT_SECRET is required")
	}
	production := authMode() != "development"
	if production && secret == developmentJWTSecret {
		return nil, errors.New("AUTH_JWT_SECRET must not use the development secret")
	}
	if len(secret) < 32 {
		return nil, errors.New("AUTH_JWT_SECRET must be at least 32 characters")
	}
	secureCookie := os.Getenv("AUTH_COOKIE_SECURE") == "true"
	requireHTTPS := os.Getenv("AUTH_REQUIRE_HTTPS") == "true"
	if production && (!secureCookie || !requireHTTPS) {
		return nil, errors.New("production requires AUTH_COOKIE_SECURE=true and AUTH_REQUIRE_HTTPS=true")
	}
	ttl := defaultJWTTTL
	if value := os.Getenv("AUTH_JWT_TTL"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return nil, errors.New("AUTH_JWT_TTL must be a positive duration")
		}
		ttl = parsed
	}
	return &jwtAuthenticator{secret: []byte(secret), issuer: getenv("AUTH_JWT_ISSUER", defaultJWTIssuer), ttl: ttl, secureCookie: secureCookie, requireHTTPS: requireHTTPS, database: database, limiter: newAuthRateLimiter(database)}, nil
}

func (a *jwtAuthenticator) middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if len(a.secret) == 0 {
			return withPrincipal(c, "local-dev", next)
		}
		if err := a.requireHTTPSRequest(c); err != nil {
			return err
		}
		claims, err := a.claimsFromRequest(c.Request())
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		}
		return withPrincipal(c, claims.Subject, next)
	}
}

func withPrincipal(c *echo.Context, subject string, next echo.HandlerFunc) error {
	c.SetRequest(c.Request().WithContext(context.WithValue(c.Request().Context(), principalKey{}, subject)))
	return next(c)
}

func principal(c *echo.Context) string {
	value, _ := c.Request().Context().Value(principalKey{}).(string)
	return value
}

func (a *jwtAuthenticator) routes(e *echo.Echo) {
	e.POST("/auth/register", a.register)
	e.POST("/auth/login", a.login)
	e.GET("/auth/session", a.currentSession)
	e.POST("/auth/logout", a.logout)
}

func (a *jwtAuthenticator) register(c *echo.Context) error {
	if err := a.requireHTTPSRequest(c); err != nil {
		return err
	}
	if !a.limiter.allow("register-ip", clientIP(c.Request()), authRegisterIPLimit, authRegisterWindow) {
		return quotaResponse(c, "too many registration attempts")
	}
	credentials, err := decodeCredentials(c)
	if err != nil {
		if errors.Is(err, http.ErrBodyReadAfterClose) || strings.Contains(err.Error(), "request body too large") {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
		}
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "email and password are required"})
	}
	if err := validateCredentials(credentials); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if !a.limiter.allow("register-account", normalizeEmail(credentials.Email), authRegisterAccountLimit, authRegisterWindow) {
		return quotaResponse(c, "too many registration attempts for this account")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(credentials.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not create account"})
	}
	user := authUser{ID: uuid.NewString(), Email: normalizeEmail(credentials.Email), PasswordHash: string(hash)}
	if err := a.database.WithContext(c.Request().Context()).Create(&user).Error; err != nil {
		return c.JSON(http.StatusConflict, map[string]string{"error": "account could not be created"})
	}
	return a.authenticate(c, user)
}

func (a *jwtAuthenticator) login(c *echo.Context) error {
	if err := a.requireHTTPSRequest(c); err != nil {
		return err
	}
	if !a.limiter.allow("login-ip", clientIP(c.Request()), authLoginIPLimit, authLoginWindow) {
		return quotaResponse(c, "too many login attempts")
	}
	credentials, err := decodeCredentials(c)
	if err != nil {
		if errors.Is(err, http.ErrBodyReadAfterClose) || strings.Contains(err.Error(), "request body too large") {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
		}
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "email and password are required"})
	}
	if err := validateCredentials(credentials); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if !a.limiter.allow("login-account", normalizeEmail(credentials.Email), authLoginAccountLimit, authLoginWindow) {
		return quotaResponse(c, "too many login attempts for this account")
	}
	var user authUser
	if err := a.database.WithContext(c.Request().Context()).Where("email = ?", normalizeEmail(credentials.Email)).First(&user).Error; err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(credentials.Password)) != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
	}
	return a.authenticate(c, user)
}

func (a *jwtAuthenticator) authenticate(c *echo.Context, user authUser) error {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwtClaims{Email: user.Email, SessionVersion: user.SessionVersion, RegisteredClaims: jwt.RegisteredClaims{Issuer: a.issuer, Subject: user.ID, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(a.ttl))}})
	value, err := token.SignedString(a.secret)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not create session"})
	}
	a.setCookie(c, value, int(a.ttl.Seconds()))
	return c.JSON(http.StatusOK, map[string]string{"subject": user.ID, "email": user.Email})
}

func (a *jwtAuthenticator) currentSession(c *echo.Context) error {
	if len(a.secret) == 0 {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
	}
	if err := a.requireHTTPSRequest(c); err != nil {
		return err
	}
	claims, err := a.claimsFromRequest(c.Request())
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
	}
	return c.JSON(http.StatusOK, map[string]string{"subject": claims.Subject, "email": claims.Email})
}

func (a *jwtAuthenticator) logout(c *echo.Context) error {
	if a.requireHTTPS {
		if err := a.requireHTTPSRequest(c); err != nil {
			return err
		}
	}
	if a.database != nil {
		claims, err := a.claimsFromRequest(c.Request())
		if err == nil {
			if err := a.database.WithContext(c.Request().Context()).Model(&authUser{}).Where("id = ?", claims.Subject).UpdateColumn("session_version", gorm.Expr("session_version + 1")).Error; err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not revoke session"})
			}
		}
	}
	a.setCookie(c, "", -1)
	return c.NoContent(http.StatusNoContent)
}

func (a *jwtAuthenticator) claimsFromRequest(request *http.Request) (*jwtClaims, error) {
	cookie, err := request.Cookie(authCookie)
	if err != nil || cookie.Value == "" {
		return nil, errors.New("missing authentication cookie")
	}
	claims := &jwtClaims{}
	token, err := jwt.ParseWithClaims(cookie.Value, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return a.secret, nil
	}, jwt.WithIssuer(a.issuer), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid || claims.Subject == "" {
		return nil, errors.New("invalid authentication cookie")
	}
	if a.database != nil {
		var user authUser
		if err := a.database.WithContext(request.Context()).Select("session_version").First(&user, "id = ?", claims.Subject).Error; err != nil || user.SessionVersion != claims.SessionVersion {
			return nil, errors.New("revoked authentication cookie")
		}
	}
	return claims, nil
}

func (a *jwtAuthenticator) setCookie(c *echo.Context, value string, maxAge int) {
	http.SetCookie(c.Response(), &http.Cookie{Name: authCookie, Value: value, Path: "/", HttpOnly: true, Secure: a.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

func decodeCredentials(c *echo.Context) (authCredentials, error) {
	var credentials authCredentials
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maximumAuthBody)
	err := json.NewDecoder(c.Request().Body).Decode(&credentials)
	return credentials, err
}

func validateCredentials(credentials authCredentials) error {
	if !strings.Contains(credentials.Email, "@") {
		return errors.New("a valid email is required")
	}
	if len([]rune(credentials.Password)) < minimumPassword {
		return errors.New("password must be at least 8 characters")
	}
	if len([]byte(credentials.Password)) > maximumPassword {
		return errors.New("password must be at most 72 bytes")
	}
	return nil
}

func (a *jwtAuthenticator) requireHTTPSRequest(c *echo.Context) error {
	if a.requireHTTPS && strings.ToLower(strings.TrimSpace(c.Request().Header.Get("X-Forwarded-Proto"))) != "https" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "HTTPS is required"})
	}
	return nil
}

func clientIP(request *http.Request) string {
	if value := strings.TrimSpace(request.Header.Get("X-Real-IP")); value != "" {
		return value
	}
	if value := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-For"), ",")[0]); value != "" {
		return value
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	if request.RemoteAddr != "" {
		return request.RemoteAddr
	}
	return "unknown"
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
