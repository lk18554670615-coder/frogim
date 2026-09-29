// Package tenancy defines the trust boundary shared by the platform and an
// enterprise. It contains no business storage or runtime tenant switching.
package tenancy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const TicketTTL = 60 * time.Second

var ErrInvalid = errors.New("invalid tenant contract")
var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

type Context struct {
	TenantID          string `json:"tenantId"`
	DisplayName       string `json:"displayName"`
	HTTPBaseURL       string `json:"httpBaseUrl"`
	AssignmentVersion int64  `json:"assignmentVersion"`
	ConfigVersion     int64  `json:"configVersion"`
}

type Identity struct {
	AccountID         string `json:"accountId"`
	TenantID          string `json:"tenantId"`
	LocalUserID       string `json:"localUserId"`
	AssignmentVersion int64  `json:"assignmentVersion"`
}

type Grant struct {
	Identity
	AuthVersion  int64     `json:"authVersion"`
	RealmVersion int64     `json:"realmVersion"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type RealmOperation struct {
	OperationID string `json:"operationId"`
	TenantID    string `json:"tenantId"`
	Version     int64  `json:"version"`
	Enabled     bool   `json:"enabled"`
}
type RealmAck struct {
	RealmOperation
	State     string `json:"state"`
	Remaining int    `json:"remaining"`
}

type CredentialOperation struct {
	OperationID string   `json:"operationId"`
	Identity    Identity `json:"identity"`
	AuthVersion int64    `json:"authVersion"`
}

type CredentialAck struct {
	CredentialOperation
	State string `json:"state"`
}

type AccessOperation struct {
	CredentialOperation
	Blocked bool `json:"blocked"`
}
type AccessAck struct {
	AccessOperation
	State string `json:"state"`
}

func ValidID(value string) bool { return idPattern.MatchString(value) }

func (i Identity) Validate() error {
	if !ValidID(i.AccountID) || !ValidID(i.TenantID) || !ValidID(i.LocalUserID) || i.AssignmentVersion < 1 {
		return ErrInvalid
	}
	return nil
}

// ValidateBaseURL rejects query/fragment/userinfo and ambiguous paths. The test
// exception is restricted to literal loopback IPs, never arbitrary HTTP hosts.
func ValidateBaseURL(raw string, allowLoopback bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || strings.TrimSpace(raw) != raw {
		return ErrInvalid
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if allowLoopback && u.Scheme == "http" && ip != nil && ip.IsLoopback() {
		return nil
	}
	return ErrInvalid
}

func (c Context) Validate(allowLoopback bool) error {
	if !ValidID(c.TenantID) || strings.TrimSpace(c.DisplayName) == "" || c.AssignmentVersion < 1 || c.ConfigVersion < 1 {
		return ErrInvalid
	}
	return ValidateBaseURL(c.HTTPBaseURL, allowLoopback)
}

// Secrets are never persisted verbatim by platform session/ticket repositories.
func Secret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func Hash(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}
