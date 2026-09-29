// tenant-import is an explicit local/offline operator tool, never a public API.
// It preserves business identities and will not resume enterprise service.
package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/linli/im/server/internal/legacyimport"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, env func(string) string, out, diagnostics io.Writer) int {
	f := flag.NewFlagSet("tenant-import", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	mode := f.String("mode", "status", "")
	batch := f.String("batch", "", "")
	tenant := f.String("tenant", "default", "")
	actor := f.String("actor", "", "")
	reason := f.String("reason", "", "")
	fingerprint := f.String("expected-fingerprint", "", "")
	confirmed := f.Bool("confirmed", false, "")
	passwordless := f.Bool("passwordless-ack", false, "")
	if e := f.Parse(args); errors.Is(e, flag.ErrHelp) {
		fmt.Fprintln(out, "Usage: tenant-import -mode status|start|resume -batch ID [-tenant default]")
		fmt.Fprintln(out, "start requires -actor ID -reason TEXT -confirmed -expected-fingerprint HEX; -passwordless-ack explicitly acknowledges accounts without a working password login.")
		fmt.Fprintln(out, "Requires TENANCY_IMPORT_ENV=development or maintenance and explicit loopback database URLs. Writes additionally require TENANCY_IMPORT_OFFLINE_CUTOVER_CONFIRMED=true. resume performs at most one account and uses TENANCY_IMPORT_CONTROL_FILE for mTLS.")
		fmt.Fprintln(out, "No adoption, schema migration, tenant activation or public route changes. Exit 0=completed, 3=durable work remains, 2=failure. Never bypass backup and obsolete-service/network cutover checks.")
		return 0
	} else if e != nil || f.NArg() != 0 || !tenancy.ValidID(*batch) || !tenancy.ValidID(*tenant) || (*mode != "status" && *mode != "start" && *mode != "resume") {
		fmt.Fprintln(diagnostics, "LEGACY_IMPORT_ARGUMENTS_INVALID")
		return 2
	}
	if env("TENANCY_IMPORT_ENV") != "development" && env("TENANCY_IMPORT_ENV") != "maintenance" {
		fmt.Fprintln(diagnostics, "LEGACY_IMPORT_LOCAL_PREVIEW_REQUIRED")
		return 2
	}
	if *mode != "status" && env("TENANCY_IMPORT_OFFLINE_CUTOVER_CONFIRMED") != "true" {
		fmt.Fprintln(diagnostics, "LEGACY_IMPORT_OFFLINE_CUTOVER_CONFIRMATION_REQUIRED")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	open := legacyimport.OpenLocalReadOnly
	if *mode != "status" {
		open = legacyimport.OpenLocalForImport
	}
	p, e := open(ctx, env("TENANCY_IMPORT_PLATFORM_DATABASE_URL"))
	if e != nil {
		fmt.Fprintln(diagnostics, "LEGACY_IMPORT_PLATFORM_UNAVAILABLE")
		return 2
	}
	defer p.Close()
	if *mode == "status" {
		b, e := legacyimport.ImportStatus(ctx, p, *batch)
		if e != nil || b.TenantID != *tenant {
			fmt.Fprintln(diagnostics, "LEGACY_IMPORT_STATUS_UNAVAILABLE")
			return 2
		}
		return emit(out, b)
	}
	s, e := open(ctx, env("TENANCY_IMPORT_SOURCE_DATABASE_URL"))
	if e != nil {
		fmt.Fprintln(diagnostics, "LEGACY_IMPORT_SOURCE_UNAVAILABLE")
		return 2
	}
	defer s.Close()
	if *mode == "start" {
		b, e := legacyimport.StartImport(ctx, s, p, legacyimport.ImportRequest{ID: *batch, TenantID: *tenant, Actor: *actor, Reason: *reason, Confirmed: *confirmed, ExpectedFingerprint: *fingerprint, AllowPasswordless: *passwordless})
		if e != nil {
			fmt.Fprintln(diagnostics, e.Error())
			return 2
		}
		return emit(out, b)
	}
	b, e := legacyimport.ImportStatus(ctx, p, *batch)
	if e != nil || b.TenantID != *tenant {
		fmt.Fprintln(diagnostics, "LEGACY_IMPORT_STATUS_UNAVAILABLE")
		return 2
	}
	if b.State == "completed" {
		return emit(out, b)
	}
	peer, e := controlPeer(env("TENANCY_IMPORT_CONTROL_FILE"), b.TenantID)
	if e != nil {
		fmt.Fprintln(diagnostics, "LEGACY_IMPORT_CONTROL_CONFIGURATION_INVALID")
		return 2
	}
	_, e = legacyimport.ResumeImportOne(ctx, s, p, *batch, platform.EnterpriseRPC{Peers: map[string]*tenancy.RPC{b.TenantID: peer}})
	if e != nil {
		fmt.Fprintln(diagnostics, e.Error())
		return 2
	}
	b, e = legacyimport.ImportStatus(ctx, p, *batch)
	if e != nil {
		fmt.Fprintln(diagnostics, "LEGACY_IMPORT_STATUS_UNAVAILABLE")
		return 2
	}
	return emit(out, b)
}
func emit(out io.Writer, b legacyimport.Batch) int {
	_ = json.NewEncoder(out).Encode(b)
	if b.State == "completed" {
		fmt.Fprintln(out, "Account import completed. Import does not resume the enterprise; cutover verification and explicit resumption are separate operations.")
		return 0
	}
	fmt.Fprintln(out, "Durable work remains; no login activation or enterprise resumption was performed.")
	return 3
}
func controlPeer(path, tenant string) (*tenancy.RPC, error) {
	if !filepath.IsAbs(path) {
		return nil, tenancy.ErrInvalid
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, tenancy.ErrInvalid
	}
	defer f.Close()
	var c struct {
		TenantID string `json:"tenantId"`
		BaseURL  string `json:"baseUrl"`
		CAFile   string `json:"caFile"`
		CertFile string `json:"certFile"`
		KeyFile  string `json:"keyFile"`
	}
	d := json.NewDecoder(io.LimitReader(f, 64<<10))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.TenantID != tenant || !filepath.IsAbs(c.CAFile) || !filepath.IsAbs(c.CertFile) || !filepath.IsAbs(c.KeyFile) {
		return nil, tenancy.ErrInvalid
	}
	u, e := url.Parse(c.BaseURL)
	if e != nil || tenancy.ValidateBaseURL(c.BaseURL, false) != nil || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, tenancy.ErrInvalid
	}
	tls, e := tenancy.TLSConfig(c.CAFile, c.CertFile, c.KeyFile)
	if e != nil {
		return nil, tenancy.ErrInvalid
	}
	leaf, e := x509.ParseCertificate(tls.Certificates[0].Certificate[0])
	if e != nil || len(leaf.URIs) != 1 || leaf.URIs[0].String() != tenancy.PlatformIdentity {
		return nil, tenancy.ErrInvalid
	}
	return tenancy.NewRPC(c.BaseURL, tls, tenancy.EnterpriseIdentity(tenant))
}
