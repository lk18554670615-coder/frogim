// Package tenancy implements a small identity directory. Business traffic never passes through it.
package tenancy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

type Options struct {
	Mode, TenantID, PlatformURL, ControlAddr, Certificate, Key, CA, StaticDir string
	PublicAPI, PublicMedia                                                    string
}

func LoadOptions() Options {
	m := os.Getenv("IM_MODE")
	if m == "" {
		m = "standalone"
	}
	return Options{m, os.Getenv("IM_TENANT_ID"), os.Getenv("IM_PLATFORM_URL"), os.Getenv("IM_CONTROL_ADDR"), os.Getenv("IM_CONTROL_CERT"), os.Getenv("IM_CONTROL_KEY"), os.Getenv("IM_CONTROL_CA"), os.Getenv("IM_PLATFORM_STATIC_DIR"), os.Getenv("IM_ENTERPRISE_API_URL"), os.Getenv("IM_ENTERPRISE_MEDIA_URL")}
}
func (o Options) Validate() error {
	if o.Mode == "standalone" {
		return nil
	}
	if o.Mode != "platform" && o.Mode != "enterprise" {
		return errors.New("IM_MODE must be standalone, platform or enterprise")
	}
	if o.ControlAddr == "" || o.Certificate == "" || o.Key == "" || o.CA == "" {
		return errors.New("tenancy requires a private mTLS listener and certificate, key, CA")
	}
	if o.Mode == "enterprise" && (!regexpCode.MatchString(o.TenantID) || !serviceURL(o.PlatformURL, "https", false) || !serviceURL(o.PublicAPI, "http", os.Getenv("IM_DEV_MODE") == "true") || !serviceURL(o.PublicMedia, "http", os.Getenv("IM_DEV_MODE") == "true")) {
		return errors.New("enterprise requires tenant ID and HTTPS platform control URL")
	}
	return nil
}

type Services struct {
	API   string `json:"apiBaseUrl"`
	IMWS  string `json:"imWsUrl"`
	IMTCP string `json:"imTcpUrl"`
	RTC   string `json:"callSignalUrl"`
	Media string `json:"mediaBaseUrl"`
}
type Tenant struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Code       string   `json:"code"`
	Enabled    bool     `json:"enabled"`
	Default    bool     `json:"isDefault"`
	Version    int64    `json:"version"`
	Services   Services `json:"services"`
	ControlURL string   `json:"controlUrl"`
}
type Profile struct {
	Name          string     `json:"name"`
	Handle        string     `json:"handle"`
	Gender        string     `json:"gender"`
	Signature     string     `json:"signature"`
	AvatarMediaID string     `json:"avatarMediaId"`
	AvatarURL     string     `json:"avatarUrl"`
	SearchHandle  bool       `json:"allowSearchByHandle"`
	SearchPhone   bool       `json:"allowSearchByPhone"`
	Banned        bool       `json:"banned"`
	CreatedAt     time.Time  `json:"createdAt"`
	DeletedAt     *time.Time `json:"deletedAt,omitempty"`
}
type User struct {
	ID       string  `json:"id"`
	Phone    string  `json:"phone"`
	TenantID string  `json:"tenantId"`
	Revision int64   `json:"assignmentVersion"`
	Banned   bool    `json:"banned"`
	Pending  string  `json:"pendingOperation,omitempty"`
	Profile  Profile `json:"profile"`
}
type Grant struct {
	Token  string `json:"ticket"`
	User   User   `json:"user"`
	Tenant Tenant `json:"tenant"`
}
type Prepare struct {
	Operation    string `json:"operationId"`
	User         User   `json:"user"`
	AvatarSource string `json:"avatarSource,omitempty"`
	AvatarGrant  string `json:"avatarGrant,omitempty"`
}
type Snapshot struct {
	UserID   string  `json:"userId"`
	Revision int64   `json:"assignmentVersion"`
	Version  int64   `json:"profileVersion"`
	Profile  Profile `json:"profile"`
}

func ID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	jsonResponse(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		fail(w, 400, "INVALID_ARGUMENT")
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		fail(w, 400, "INVALID_ARGUMENT")
		return false
	}
	return true
}
func openPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	c, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, e
	}
	c.MaxConns = 5
	c.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	return pgxpool.NewWithConfig(ctx, c)
}
func TLS(o Options) (*tls.Config, error) {
	cert, e := tls.LoadX509KeyPair(o.Certificate, o.Key)
	if e != nil {
		return nil, e
	}
	pem, e := os.ReadFile(o.CA)
	if e != nil {
		return nil, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid control CA")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: roots, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}
func ControlClient(o Options) (*http.Client, error) {
	c, e := TLS(o)
	if e != nil {
		return nil, e
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: c}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("control redirects forbidden") }}, nil
}
func peer(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	return r.TLS.PeerCertificates[0].Subject.CommonName
}
func serviceURL(s, kind string, dev bool) bool {
	u, e := url.Parse(s)
	if e != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if kind == "tcp" {
		return u.Scheme == "tcp" || u.Scheme == "tls"
	}
	if kind == "ws" {
		return u.Scheme == "wss" || (dev && u.Scheme == "ws")
	}
	return u.Scheme == "https" || (dev && u.Scheme == "http")
}
