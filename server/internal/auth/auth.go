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
	TokenType   string `json:"typ"`
	Role        string `json:"role,omitempty"`
	AuthVersion int64  `json:"ver,omitempty"`
	SessionID   string `json:"sid,omitempty"`
	DeviceKind  string `json:"dev,omitempty"`
	jwt.RegisteredClaims
}

func (m Manager) IssueAdmin(adminID, role string, ttl time.Duration, authVersion int64) (string, error) {
	now := time.Now()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{TokenType: "admin", Role: role, AuthVersion: authVersion, RegisteredClaims: jwt.RegisteredClaims{Subject: adminID, ID: hex.EncodeToString(b[:]), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)), ExpiresAt: jwt.NewNumericDate(now.Add(ttl))}}).SignedString(m.Secret)
}

func (m Manager) Issue(userID string) (string, string, error) {
	access, err := m.sign(userID, "access", m.AccessTTL, "", "")
	if err != nil {
		return "", "", err
	}
	refresh, err := m.sign(userID, "refresh", m.RefreshTTL, "", "")
	return access, refresh, err
}

// IssueDeviceSession creates one logical business session for a WuKongIM
// device category. Android and iOS intentionally share the app category.
func (m Manager) IssueDeviceSession(userID, deviceKind string) (string, string, string, error) {
	sessionID, err := randomID()
	if err != nil {
		return "", "", "", err
	}
	access, refresh, err := m.issueDeviceTokens(userID, sessionID, deviceKind)
	return access, refresh, sessionID, err
}

// RotateDeviceSession preserves the logical session while rotating both token
// JTIs. This lets the persistence layer invalidate an old same-type login
// without invalidating Web or desktop sessions for the same user.
func (m Manager) RotateDeviceSession(userID, sessionID, deviceKind string) (string, string, error) {
	if sessionID == "" || deviceKind == "" {
		return "", "", errors.New("device session is required")
	}
	return m.issueDeviceTokens(userID, sessionID, deviceKind)
}

func (m Manager) issueDeviceTokens(userID, sessionID, deviceKind string) (string, string, error) {
	access, err := m.sign(userID, "access", m.AccessTTL, sessionID, deviceKind)
	if err != nil {
		return "", "", err
	}
	refresh, err := m.sign(userID, "refresh", m.RefreshTTL, sessionID, deviceKind)
	return access, refresh, err
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (m Manager) sign(userID, typ string, ttl time.Duration, sessionID, deviceKind string) (string, error) {
	now := time.Now()
	id, err := randomID()
	if err != nil {
		return "", err
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{TokenType: typ, SessionID: sessionID, DeviceKind: deviceKind, RegisteredClaims: jwt.RegisteredClaims{Subject: userID, ID: id, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)), ExpiresAt: jwt.NewNumericDate(now.Add(ttl))}}).SignedString(m.Secret)
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
