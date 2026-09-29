package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/deployment"
)

func executeMediaRepair(ctx context.Context, configPath, keyPath string, confirmed bool) (string, error) {
	if !confirmed || keyPath != "" {
		return "", backup.ErrInvalid
	}
	c, e := readConfiguration(configPath)
	if e != nil {
		return "", e
	}
	if c.MediaRepair == nil || c.ArchiveDirectory != "" || c.RestoreReleaseID != "" || c.RestoreSequence != 0 || !c.Expected.Valid() || c.Expected.Scope != "" {
		return "", backup.ErrInvalid
	}
	for _, p := range []string{c.StateDirectory, c.BundleDirectory, c.DockerBinary} {
		if !filepath.IsAbs(p) {
			return "", backup.ErrInvalid
		}
	}
	if _, e = readRegular(filepath.Join(c.StateDirectory, "state.json"), 8<<20); e != nil {
		return "", backup.ErrInvalid
	}
	raw, _ := json.Marshal(c.Catalog)
	catalog, e := deployment.ReadCatalog(bytes.NewReader(raw))
	if e != nil {
		return "", backup.ErrInvalid
	}
	release, ok := catalog[c.Expected.ReleaseID]
	if !ok {
		return "", backup.ErrInvalid
	}
	runner := &deployment.ComposeRunner{Binary: c.DockerBinary, Endpoint: c.DockerEndpoint, BundleRoot: c.BundleDirectory, Project: "frogim-deploy-" + c.Expected.TenantID, Server: c.Expected.ServerID, Tenant: c.Expected.TenantID, Catalog: catalog, IsolationMode: release.IsolationMode}
	x, e := deployment.OpenExecutor(c.StateDirectory, runner.Server, runner.Tenant, c.HostFingerprint, c.Runtime, catalog, runner)
	if e != nil {
		return "", e
	}
	defer x.Close()
	result, e := x.RepairColdMedia(ctx, c.Expected, *c.MediaRepair)
	if e != nil {
		return "", e
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}
