package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
)

// Configure before serving. The private, operator-owned file is never supplied
// through HTTP. No execution capability is installed by default.
func (a *Agent) ConfigureExecutor(path string) error {
	if !filepath.IsAbs(path) || a.executor != nil {
		return tenancy.ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return ErrState
	}
	defer f.Close()
	var c struct {
		StateDirectory  string    `json:"stateDirectory"`
		BundleDirectory string    `json:"bundleDirectory"`
		DockerBinary    string    `json:"dockerBinary"`
		DockerEndpoint  string    `json:"dockerEndpoint"`
		Catalog         []Release `json:"catalog"`
		Backup          *struct {
			Directory string                `json:"directory"`
			KeyFile   string                `json:"keyFile"`
			Offsite   *backup.OffsiteConfig `json:"offsite,omitempty"`
		} `json:"backup,omitempty"`
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return tenancy.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return tenancy.ErrInvalid
	}
	catalog := map[string]Release{}
	for _, r := range c.Catalog {
		if _, found := catalog[r.ID]; found {
			return tenancy.ErrInvalid
		}
		catalog[r.ID] = r
	}
	r := a.report
	runner := ComposeRunner{Binary: c.DockerBinary, Endpoint: c.DockerEndpoint, BundleRoot: c.BundleDirectory, Project: "frogim-deploy-" + r.TenantID, Server: r.ServerID, Tenant: r.TenantID, Catalog: catalog}
	runner.IsolationMode = r.IsolationMode
	for _, release := range catalog {
		if release.isolationMode() != r.IsolationMode {
			return tenancy.ErrInvalid
		}
	}
	if !runner.valid() {
		return tenancy.ErrInvalid
	}
	x, err := OpenExecutor(c.StateDirectory, r.ServerID, r.TenantID, r.HostFingerprint, r.Runtime, catalog, &runner)
	if err != nil {
		return err
	}
	if c.Backup != nil {
		if r.IsolationMode == "dedicated_host" && c.Backup.Offsite == nil {
			x.Close()
			return ErrState
		}
		if err = x.ConfigureBackups(c.Backup.Directory, c.Backup.KeyFile); err != nil {
			x.Close()
			return err
		}
		if c.Backup.Offsite != nil {
			if err = x.ConfigureOffsite(*c.Backup.Offsite); err != nil {
				x.Close()
				return err
			}
		} else if x.state.OffsiteConfiguration != "" {
			x.Close()
			return ErrState
		}
	} else if x.state.BackupConfiguration != "" {
		// Restarting with missing backup configuration must not strand a recovery
		// job while advertising a healthy deployment-only executor.
		x.Close()
		return ErrState
	}
	a.executor = x
	a.report.Capabilities = []string{"inspect", "deploy"}
	if c.Backup != nil {
		a.report.Capabilities = append(a.report.Capabilities, "backup")
	}
	return nil
}

type ControlRequest struct {
	Nonce            string    `json:"nonce"`
	Operation        Operation `json:"operation"`
	ExpectedAttempts int       `json:"expectedAttempts"`
}
type ControlReport struct {
	Nonce   string         `json:"nonce"`
	Status  ExecutorStatus `json:"status"`
	Receipt Receipt        `json:"receipt"`
}

// Only the platform's independent mTLS principal can dispatch these immutable
// release operations. An enterprise certificate cannot dispatch work.
func (a *Agent) deploymentRoutes(mux *http.ServeMux) {
	for _, action := range []string{"status", "submit", "retry"} {
		mux.HandleFunc("POST /internal/agent/deployment/"+action, func(w http.ResponseWriter, r *http.Request) {
			var in ControlRequest
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if r.Header.Get("Content-Type") != "application/json" || d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || !noncePattern.MatchString(in.Nonce) || in.ExpectedAttempts < 0 {
				w.WriteHeader(400)
				return
			}
			var receipt Receipt
			var err error
			if action == "submit" {
				receipt, err = a.executor.Submit(in.Operation)
			}
			if action == "retry" {
				_, prior, e := a.executor.Status(in.Operation.ID)
				if e != nil || prior.Operation != in.Operation {
					w.WriteHeader(409)
					return
				}
				receipt, err = a.executor.Retry(in.Operation.ID, in.ExpectedAttempts)
			}
			if err != nil {
				w.WriteHeader(409)
				return
			}
			status, current, err := a.executor.Status(in.Operation.ID)
			if err != nil {
				w.WriteHeader(409)
				return
			}
			if in.Operation.ID != "" {
				receipt = current
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ControlReport{Nonce: in.Nonce, Status: status, Receipt: receipt})
		})
	}
}
func (a *Agent) RunExecutor(ctx context.Context) {
	if a.executor == nil {
		return
	}
	// Network delivery is independent of local service maintenance. Do not let
	// a slow provider prevent the next queued service recovery from running.
	var delivery sync.WaitGroup
	delivery.Add(1)
	go func() {
		defer delivery.Done()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_, _ = a.executor.OffsiteOnce(ctx)
			}
		}
	}()
	defer delivery.Wait()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = a.executor.Once(ctx)
			if ctx.Err() == nil {
				_, _ = a.executor.BackupOnce(ctx)
			}
		}
	}
}
func (a *Agent) Close() {
	if a.executor != nil {
		a.executor.Close()
	}
}
