// Offline, operator-only cold backup/staged restore. No platform/agent HTTP
// endpoint, schedule, automatic suspension or automatic activation is added.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/privatefile"
)

type configuration struct {
	Expected         backup.Binding                 `json:"expected"`
	HostFingerprint  string                         `json:"hostFingerprint"`
	Runtime          string                         `json:"runtime"`
	StateDirectory   string                         `json:"stateDirectory"`
	BundleDirectory  string                         `json:"bundleDirectory"`
	DockerBinary     string                         `json:"dockerBinary"`
	DockerEndpoint   string                         `json:"dockerEndpoint"`
	Catalog          []deployment.Release           `json:"catalog"`
	ArchiveDirectory string                         `json:"archiveDirectory"`
	RestoreReleaseID string                         `json:"restoreReleaseId,omitempty"`
	RestoreSequence  int64                          `json:"restoreSequence,omitempty"`
	MediaRepair      *deployment.MediaRepairRequest `json:"mediaRepair,omitempty"`
}

func readRegular(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, backup.ErrInvalid
	}
	i, e := os.Lstat(path)
	if e != nil || !i.Mode().IsRegular() || i.Size() > limit {
		return nil, backup.ErrInvalid
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, backup.ErrInvalid
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil || !os.SameFile(i, actual) {
		return nil, backup.ErrInvalid
	}
	data, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(data)) > limit {
		return nil, backup.ErrInvalid
	}
	return data, nil
}
func readConfiguration(path string) (configuration, error) {
	var c configuration
	data, e := readRegular(path, 1<<20)
	if e != nil {
		return c, e
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return configuration{}, backup.ErrInvalid
	}
	return c, nil
}
func execute(ctx context.Context, mode, configPath, keyPath string, confirmed bool) (string, error) {
	if mode == "repair-media" {
		return executeMediaRepair(ctx, configPath, keyPath, confirmed)
	}
	if confirmed && mode == "create-key" && configPath == "" && filepath.IsAbs(keyPath) {
		var key [32]byte
		if _, e := rand.Read(key[:]); e != nil {
			return "", backup.ErrInvalid
		}
		defer clear(key[:])
		f, e := privatefile.Create(keyPath)
		if e != nil {
			return "", backup.ErrInvalid
		}
		defer f.Close()
		if n, e := f.Write(key[:]); e != nil || n != len(key) || f.Sync() != nil {
			return "", backup.ErrInvalid
		}
		return "Independent backup key created. Keep it separate from encrypted archives and authentication keys.", nil
	}
	if !confirmed || (mode != "backup" && mode != "stage-restore") {
		return "", backup.ErrInvalid
	}
	c, e := readConfiguration(configPath)
	if e != nil {
		return "", e
	}
	if c.MediaRepair != nil {
		return "", backup.ErrInvalid
	}
	for _, p := range []string{c.StateDirectory, c.BundleDirectory, c.DockerBinary, c.ArchiveDirectory, keyPath} {
		if !filepath.IsAbs(p) {
			return "", backup.ErrInvalid
		}
	}
	// A restore key must not be shipped inside the backup it protects.
	keyParent, e := filepath.EvalSymlinks(filepath.Dir(keyPath))
	if e != nil {
		return "", backup.ErrInvalid
	}
	archiveParent, e := filepath.EvalSymlinks(filepath.Dir(c.ArchiveDirectory))
	if e != nil {
		return "", backup.ErrInvalid
	}
	archivePath := filepath.Join(archiveParent, filepath.Base(c.ArchiveDirectory))
	resolvedKey := filepath.Join(keyParent, filepath.Base(keyPath))
	if info, e := os.Lstat(archivePath); e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", backup.ErrInvalid
	}
	if strings.EqualFold(filepath.VolumeName(archivePath), filepath.VolumeName(resolvedKey)) {
		rel, e := filepath.Rel(archivePath, resolvedKey)
		if e != nil || rel == "." || (rel != ".." && !bytes.HasPrefix([]byte(rel), []byte(".."+string(filepath.Separator)))) {
			return "", backup.ErrInvalid
		}
	}
	key, e := readRegular(keyPath, 32)
	if e != nil || len(key) != 32 {
		return "", backup.ErrInvalid
	}
	defer clear(key)
	// OpenExecutor normally initializes absent journals for first deployment.
	// This tool must NOT use that path or adopt an unknown existing stack.
	if _, e = readRegular(filepath.Join(c.StateDirectory, "state.json"), 8<<20); e != nil {
		return "", backup.ErrInvalid
	}
	catalogJSON, _ := json.Marshal(c.Catalog)
	catalog, e := deployment.ReadCatalog(bytes.NewReader(catalogJSON))
	if e != nil {
		return "", backup.ErrInvalid
	}
	release, ok := catalog[c.Expected.ReleaseID]
	if !ok || c.Expected.Scope != "" {
		return "", backup.ErrInvalid
	}
	r := &deployment.ComposeRunner{Binary: c.DockerBinary, Endpoint: c.DockerEndpoint, BundleRoot: c.BundleDirectory, Project: "frogim-deploy-" + c.Expected.TenantID, Server: c.Expected.ServerID, Tenant: c.Expected.TenantID, Catalog: catalog, IsolationMode: release.IsolationMode}
	x, e := deployment.OpenExecutor(c.StateDirectory, r.Server, r.Tenant, c.HostFingerprint, c.Runtime, catalog, r)
	if e != nil {
		return "", e
	}
	defer x.Close()
	if mode == "backup" {
		if c.RestoreReleaseID != "" || c.RestoreSequence != 0 {
			return "", backup.ErrInvalid
		}
		if e = x.ColdBackup(ctx, c.Expected, c.ArchiveDirectory, key); e != nil {
			return "", e
		}
		return "Encrypted cold backup complete. No service was started or changed.", nil
	}
	restored, e := x.StageColdRestore(ctx, c.Expected, c.ArchiveDirectory, key, c.RestoreReleaseID, c.RestoreSequence)
	if e != nil {
		return "", e
	}
	return "Restored to new volumes; staged release " + restored.ID + ". NOT activated; original volumes retained.", nil
}
func main() {
	mode := flag.String("mode", "", "create-key, backup, stage-restore or repair-media (cold, paused enterprise only)")
	config := flag.String("config", "", "absolute private operator configuration path")
	key := flag.String("key-file", "", "absolute external 32-byte binary backup key path")
	confirmed := flag.Bool("confirmed", false, "operator confirms maintenance, stopped writers and existing journal")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "cold backup operation rejected")
		os.Exit(2)
	}
	message, e := execute(ctx, *mode, *config, *key, *confirmed)
	if e != nil {
		if *mode == "repair-media" {
			fmt.Fprintln(os.Stderr, "Cold media repair unconfirmed. Keep the enterprise paused and original writers stopped; retain its journal and inspect/retry the same request. No service restart or access activation was performed.")
		} else {
			fmt.Fprintln(os.Stderr, "cold backup/restore unconfirmed; no automatic activation or cleanup of original data performed")
		}
		os.Exit(1)
	}
	fmt.Println(message)
}
