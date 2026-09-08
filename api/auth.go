package main

import (
	"context"
	"encoding/json"
	"errors"
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
	authCookie       = "documind_token"
	defaultJWTIssuer = "documind"
	defaultJWTTTL    = 8 * time.Hour
	minimumPassword  = 8
)

type authUser struct {
	ID           string `gorm:"type:uuid;primaryKey"`
	Email        string `gorm:"size:320;uniqueIndex;not null"`
	PasswordHash string `gorm:"not null"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type jwtAuthenticator struct {
	secret       []byte
	issuer       string
	ttl          time.Duration
	secureCookie bool
	database     *gorm.DB
}

type authCredentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type jwtClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

func newAuthenticator(database *gorm.DB) (*jwtAuthenticator, error) {
	secret := os.Getenv("AUTH_JWT_SECRET")
	if database == nil && secret == "" {
		return &jwtAuthenticator{}, nil
	}
	if secret == "" {
		if authMode() == "development" && database == nil {
			return &jwtAuthenticator{}, nil
		}
		return nil, errors.New("AUTH_JWT_SECRET is required")
	}
	if len(secret) < 32 {
		return nil, errors.New("AUTH_JWT_SECRET must be at least 32 characters")
	}
	ttl := defaultJWTTTL
	if value := os.Getenv("AUTH_JWT_TTL"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return nil, errors.New("AUTH_JWT_TTL must be a positive duration")
		}
		ttl = parsed
	}
	return &jwtAuthenticator{secret: []byte(secret), issuer: getenv("AUTH_JWT_ISSUER", defaultJWTIssuer), ttl: ttl, secureCookie: os.Getenv("AUTH_COOKIE_SECURE") == "true", database: database}, nil
}

func (a *jwtAuthenticator) middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if len(a.secret) == 0 {
			return withPrincipal(c, "local-dev", next)
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
	credentials, err := decodeCredentials(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "email and password are required"})
	}
	if err := validateCredentials(credentials); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
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
	credentials, err := decodeCredentials(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "email and password are required"})
	}
	var user authUser
	if err := a.database.WithContext(c.Request().Context()).Where("email = ?", normalizeEmail(credentials.Email)).First(&user).Error; err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(credentials.Password)) != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
	}
	return a.authenticate(c, user)
}

func (a *jwtAuthenticator) authenticate(c *echo.Context, user authUser) error {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwtClaims{Email: user.Email, RegisteredClaims: jwt.RegisteredClaims{Issuer: a.issuer, Subject: user.ID, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(a.ttl))}})
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
	claims, err := a.claimsFromRequest(c.Request())
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
	}
	return c.JSON(http.StatusOK, map[string]string{"subject": claims.Subject, "email": claims.Email})
}

func (a *jwtAuthenticator) logout(c *echo.Context) error {
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
	return claims, nil
}

func (a *jwtAuthenticator) setCookie(c *echo.Context, value string, maxAge int) {
	http.SetCookie(c.Response(), &http.Cookie{Name: authCookie, Value: value, Path: "/", HttpOnly: true, Secure: a.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

func decodeCredentials(c *echo.Context) (authCredentials, error) {
	var credentials authCredentials
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
	return nil
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
