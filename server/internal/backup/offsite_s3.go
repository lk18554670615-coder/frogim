package backup

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Exactly ONE authority per private config/credential. Public requests never
// supply endpoint, bucket, prefix, credential, CA or archive filesystem paths.
type OffsiteConfig struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	TenantID    string `json:"tenantId,omitempty"`
	ServerID    string `json:"serverId,omitempty"`
	DirectoryID string `json:"directoryId,omitempty"`
	Endpoint    string `json:"endpoint"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	Region      string `json:"region"`
	AccessKey   string `json:"accessKey"`
	SecretKey   string `json:"secretKey"`
	CAFile      string `json:"caFile,omitempty"`
}

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
var regionName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (c OffsiteConfig) valid() bool {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || c.Endpoint != "https://"+u.Host || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Host != strings.ToLower(u.Host) {
		return false
	}
	if !safeName.MatchString(c.ID) || !bucketName.MatchString(c.Bucket) || !safeName.MatchString(c.Prefix) || !regionName.MatchString(c.Region) || len(c.AccessKey) < 3 || len(c.AccessKey) > 128 || len(c.SecretKey) < 32 || len(c.SecretKey) > 256 || strings.ContainsAny(c.AccessKey+c.SecretKey, "\r\n\x00") {
		return false
	}
	if c.CAFile != "" && !filepath.IsAbs(c.CAFile) {
		return false
	}
	if c.Scope == "platform" {
		return directoryID.MatchString(c.DirectoryID) && c.TenantID == "" && c.ServerID == ""
	}
	return c.Scope == "enterprise" && safeName.MatchString(c.TenantID) && safeName.MatchString(c.ServerID) && c.DirectoryID == ""
}
func (c OffsiteConfig) matches(b Binding) bool {
	if !c.valid() || !b.Valid() {
		return false
	}
	if c.Scope == "platform" {
		return b.Scope == "platform" && c.DirectoryID == b.DirectoryID
	}
	return b.Scope == "" && c.TenantID == b.TenantID && c.ServerID == b.ServerID
}
func (o *Offsite) Accepts(b Binding) bool { return o != nil && o.config.matches(b) }

// Validate key separation during private worker configuration, before any
// scheduled cold backup can pause services.
func (o *Offsite) IndependentKey(key []byte) bool { return o != nil && o.independentKey(key) }
func (o *Offsite) independentKey(key []byte) bool {
	if len(key) != 32 {
		return false
	}
	for _, encoded := range []string{base64.RawURLEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString(key), hex.EncodeToString(key), string(key)} {
		if o.config.SecretKey == encoded || o.config.AccessKey == encoded {
			return false
		}
	}
	return true
}
func (c OffsiteConfig) objectRoot() string {
	if c.Scope == "platform" {
		return c.Prefix + "/platform/" + c.DirectoryID
	}
	return c.Prefix + "/enterprise/" + c.TenantID + "/" + c.ServerID
}

// IAMPolicy provides the exact data permissions needed for one independently
// provisioned credential. It does not create a bucket/user, grant permissions,
// enable public access or assume the real provider applied the policy.
func (c OffsiteConfig) IAMPolicy() ([]byte, error) {
	if !c.valid() {
		return nil, ErrInvalid
	}
	policy := map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{
		"Effect": "Allow", "Action": []string{"s3:GetObject", "s3:PutObject"},
		"Resource": []string{"arn:aws:s3:::" + c.Bucket + "/" + c.objectRoot() + "/*"},
	}}}
	return json.MarshalIndent(policy, "", "  ")
}

func NewOffsite(c OffsiteConfig) (*Offsite, error) {
	if !c.valid() {
		return nil, ErrInvalid
	}
	u, _ := url.Parse(c.Endpoint)
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CAFile != "" {
		info, e := os.Lstat(c.CAFile)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil, ErrInvalid
		}
		data, e := os.ReadFile(c.CAFile)
		if e != nil {
			return nil, ErrInvalid
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(data) {
			return nil, ErrInvalid
		}
		tlsConfig.RootCAs = roots
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConnsPerHost: 2}
	client, e := minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""), Secure: true, Region: c.Region, BucketLookup: minio.BucketLookupPath, Transport: fixedOriginTransport{origin: u.Host, next: transport}, MaxRetries: 1})
	if e != nil {
		return nil, ErrInvalid
	}
	return &Offsite{config: c, objects: &s3Objects{client: client, bucket: c.Bucket, transport: transport}}, nil
}
func (o *Offsite) Close() {
	if s, ok := o.objects.(*s3Objects); ok {
		s.transport.CloseIdleConnections()
	}
}

type fixedOriginTransport struct {
	origin string
	next   http.RoundTripper
}

func (t fixedOriginTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Also blocks SDK-internal redirects to other hosts, not just net/http's
	// redirect callback. Authentication must never follow a storage redirect.
	if r.URL.Scheme != "https" || r.URL.Host != t.origin || r.URL.User != nil {
		return nil, ErrOffsite
	}
	return t.next.RoundTrip(r)
}

type s3Objects struct {
	client    *minio.Client
	bucket    string
	transport *http.Transport
}

func (s *s3Objects) get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, e := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if e != nil {
		return nil, ErrOffsite
	}
	if _, e = obj.Stat(); e != nil {
		obj.Close()
		if minio.ToErrorResponse(e).Code == "NoSuchKey" {
			return nil, errObjectAbsent
		}
		return nil, ErrOffsite
	}
	return obj, nil
}
func (s *s3Objects) putNew(ctx context.Context, key string, data []byte) error {
	options := minio.PutObjectOptions{ContentType: "application/octet-stream", DisableMultipart: true, SendContentMd5: true}
	options.SetMatchETagExcept("*")
	_, e := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), options)
	if e != nil {
		if minio.ToErrorResponse(e).Code == "PreconditionFailed" {
			return errObjectExists
		}
		return ErrOffsite
	}
	return nil
}
