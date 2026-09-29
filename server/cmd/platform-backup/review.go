package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"

	"github.com/linli/im/server/internal/directorybackup"
	"github.com/linli/im/server/internal/tenancy"
)

type reviewConfiguration struct {
	DatabaseURL     string `json:"databaseUrl"`
	CAFile          string `json:"caFile"`
	CertificateFile string `json:"certificateFile"`
	PrivateKeyFile  string `json:"privateKeyFile"`
	directorybackup.ReviewRequest
}

func executeReview(ctx context.Context, mode, configPath, keyPath string, confirmed bool) (string, error) {
	if mode != "recovery-review" && mode != "recovery-review-status" || keyPath != "" || mode == "recovery-review" && !confirmed {
		return "", directorybackup.ErrInvalid
	}
	raw, e := readRegular(configPath, 1<<20)
	if e != nil {
		return "", e
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var c reviewConfiguration
	if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF {
		return "", directorybackup.ErrInvalid
	}
	db := directorybackup.Database{DSN: c.DatabaseURL}
	var out directorybackup.ReviewResult
	if mode == "recovery-review-status" {
		out, e = db.ReviewStatus(ctx, c.ReviewRequest)
	} else {
		if !filepath.IsAbs(c.CAFile) || !filepath.IsAbs(c.CertificateFile) || !filepath.IsAbs(c.PrivateKeyFile) {
			return "", directorybackup.ErrInvalid
		}
		tlsConfig, err := tenancy.TLSConfig(c.CAFile, c.CertificateFile, c.PrivateKeyFile)
		if err != nil || tenancy.ValidateLocalIdentity(tlsConfig, tenancy.PlatformIdentity) != nil {
			return "", directorybackup.ErrInvalid
		}
		peers := map[string]*tenancy.RPC{}
		for _, p := range c.Peers {
			peer, err := tenancy.NewRPC(p.ControlURL, tlsConfig, tenancy.EnterpriseIdentity(p.TenantID))
			if err != nil {
				return "", directorybackup.ErrInvalid
			}
			peers[p.TenantID] = peer
		}
		out, e = db.Review(ctx, c.ReviewRequest, func(work context.Context, p directorybackup.ReviewPeer) (tenancy.RecoveryInventory, string, error) {
			return tenancy.CollectRecoveryInventory(work, p.TenantID, p.HTTPBaseURL, p.RealmVersion, func(work context.Context, request tenancy.RecoveryInventoryRequest) (tenancy.RecoveryInventoryPage, error) {
				var page tenancy.RecoveryInventoryPage
				err := peers[p.TenantID].Call(work, "/internal/tenancy/recovery/inventory", request, &page)
				return page, err
			})
		})
	}
	if e != nil {
		return "", e
	}
	result, e := json.Marshal(out)
	if e != nil {
		return "", directorybackup.ErrUnconfirmed
	}
	return string(result), nil
}
