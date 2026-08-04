package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Manager struct {
	Secret                []byte
	AccessTTL, RefreshTTL time.Duration
}

type Claims struct {
	TokenType string `json:"typ"`
	Role      string `json:"role,omitempty"`
	jwt.RegisteredClaims
}

func (m Manager) IssueAdmin(adminID, role string, ttl time.Duration) (string, error) {
	now := time.Now()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{TokenType: "admin", Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: adminID, ID: hex.EncodeToString(b[:]), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)), ExpiresAt: jwt.NewNumericDate(now.Add(ttl))}}).SignedString(m.Secret)
}

func (m Manager) Issue(userID string) (string, string, error) {
	access, err := m.sign(userID, "access", m.AccessTTL)
	if err != nil {
		return "", "", err
	}
	refresh, err := m.sign(userID, "refresh", m.RefreshTTL)
	return access, refresh, err
}

// IssueWebSocket creates a short-lived, single-use admission ticket. Its brief
// lifetime and atomic consumption limit exposure; callers and gateways must
// still treat it as a credential and redact it from access logs.
func (m Manager) IssueWebSocket(userID string, ttl time.Duration) (string, error) {
	return m.sign(userID, "ws", ttl)
}

func (m Manager) sign(userID, typ string, ttl time.Duration) (string, error) {
	now := time.Now()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{TokenType: typ, RegisteredClaims: jwt.RegisteredClaims{Subject: userID, ID: hex.EncodeToString(b[:]), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)), ExpiresAt: jwt.NewNumericDate(now.Add(ttl))}}).SignedString(m.Secret)
}

func (m Manager) Parse(raw, expected string) (string, error) {
	c, err := m.ParseClaims(raw, expected)
	if err != nil {
		return "", err
	}
	return c.Subject, nil
}

func (m Manager) ParseClaims(raw, expected string) (*Claims, error) {
	t, err := jwt.ParseWithClaims(raw, &Claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return m.Secret, nil
	})
	if err != nil || !t.Valid {
		return nil, errors.New("invalid token")
	}
	c, ok := t.Claims.(*Claims)
	if !ok || c.TokenType != expected || c.Subject == "" {
		return nil, errors.New("invalid token claims")
	}
	return c, nil
}
