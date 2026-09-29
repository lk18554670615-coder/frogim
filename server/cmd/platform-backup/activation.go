package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"

	"github.com/linli/im/server/internal/directorybackup"
	"github.com/linli/im/server/internal/tenancy"
)

type activationConfiguration struct {
	directorybackup.ActivationRequest
	DatabaseURL              string            `json:"databaseUrl"`
	CAFile                   string            `json:"caFile"`
	CertificateFile          string            `json:"certificateFile"`
	PrivateKeyFile           string            `json:"privateKeyFile"`
	OldCAFile                string            `json:"oldCaFile"`
	AdminPasswordHashFile    string            `json:"adminPasswordHashFile"`
	AccountPasswordHashFiles map[string]string `json:"accountPasswordHashFiles"`
}

func executeActivation(ctx context.Context, mode, path, key string, confirmed bool) (string, error) {
	if key != "" || (mode != "recovery-prepare" && mode != "recovery-activate" && mode != "recovery-status") || mode != "recovery-status" && !confirmed {
		return "", directorybackup.ErrInvalid
	}
	raw, e := readRegular(path, 1<<20)
	if e != nil {
		return "", e
	}
	defer clear(raw)
	var c activationConfiguration
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF {
		return "", directorybackup.ErrInvalid
	}
	db := directorybackup.Database{DSN: c.DatabaseURL}
	var result directorybackup.ActivationResult
	if mode == "recovery-status" {
		result, e = db.ActivationStatus(ctx, c.ActivationRequest)
	} else {
		loadHash := func(path string) (string, error) {
			raw, e := readRegular(path, 128)
			if e != nil {
				return "", e
			}
			defer clear(raw)
			return strings.TrimSpace(string(raw)), nil
		}
		if c.Mode == "" {
			c.AdminPasswordHash, e = loadHash(c.AdminPasswordHashFile)
			if e != nil {
				return "", e
			}
		}
		if len(c.AccountPasswordHashFiles) != len(c.Accounts) {
			return "", directorybackup.ErrInvalid
		}
		for index := range c.Accounts {
			a := &c.Accounts[index]
			a.PasswordHash, e = loadHash(c.AccountPasswordHashFiles[a.Identity.AccountID])
			if e != nil {
				return "", e
			}
		}
		if !filepath.IsAbs(c.CAFile) || !filepath.IsAbs(c.CertificateFile) || !filepath.IsAbs(c.PrivateKeyFile) {
			return "", directorybackup.ErrInvalid
		}
		config, e := tenancy.TLSConfig(c.CAFile, c.CertificateFile, c.PrivateKeyFile)
		if e != nil || tenancy.ValidateLocalIdentity(config, tenancy.PlatformIdentity) != nil || tenancy.CertificateFingerprint(config) != c.AuthoritySHA256 {
			return "", directorybackup.ErrInvalid
		}
		old, e := readRegular(c.OldCAFile, 1<<20)
		if e != nil {
			return "", e
		}
		h := sha256.Sum256(old)
		if hex.EncodeToString(h[:]) != c.OldAuthoritiesSHA256 {
			return "", directorybackup.ErrInvalid
		}
		roots, e := tenancy.CaptureAuthorities(config, c.CAFile)
		if e != nil || !tenancy.DisjointAuthorities(string(old), roots) {
			return "", directorybackup.ErrInvalid
		}
		peers := map[string]*tenancy.RPC{}
		for _, p := range c.Peers {
			peer, e := tenancy.NewRPC(p.ControlURL, config, tenancy.EnterpriseIdentity(p.TenantID))
			if e != nil {
				return "", e
			}
			peers[p.TenantID] = peer
		}
		if mode == "recovery-prepare" {
			result, e = db.PrepareActivation(ctx, c.ActivationRequest)
		} else {
			read := func(work context.Context, p directorybackup.ReviewPeer) (tenancy.RecoveryInventory, string, error) {
				return tenancy.CollectRecoveryInventory(work, p.TenantID, p.HTTPBaseURL, p.RealmVersion, func(work context.Context, q tenancy.RecoveryInventoryRequest) (tenancy.RecoveryInventoryPage, error) {
					var out tenancy.RecoveryInventoryPage
					e := peers[p.TenantID].Call(work, "/internal/tenancy/recovery/inventory", q, &out)
					return out, e
				})
			}
			verify := func(work context.Context, p directorybackup.ReviewPeer) error {
				nonce, e := tenancy.Secret()
				if e != nil {
					return e
				}
				var proof tenancy.RecoveryAuthority
				if e = peers[p.TenantID].Call(work, "/internal/tenancy/recovery/authority", tenancy.RecoveryInventoryRequest{Nonce: nonce, TenantID: p.TenantID, RealmVersion: p.RealmVersion}, &proof); e != nil {
					return e
				}
				if proof.TenantID != p.TenantID || proof.PlatformControlURL != c.PlatformControlURL || proof.ClientCertificateSHA256 != c.AuthoritySHA256 || !tenancy.DisjointAuthorities(string(old), proof.AuthoritiesPEM) {
					return directorybackup.ErrUnconfirmed
				}
				return nil
			}
			result, e = db.Activate(ctx, c.ActivationRequest, read, verify)
		}
		if e != nil {
			return "", e
		}
	}
	if e != nil {
		return "", e
	}
	out, e := json.Marshal(result)
	return string(out), e
}
