package deployment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	"github.com/linli/im/server/internal/backup"
)

var coldVolumes = []string{"im", "im-logs", "media", "plugins", "redis"}

// ColdBackup is an offline operator core, not a public/agent endpoint. The
// platform maintenance job and suspension must be confirmed first; ALL writers
// except PostgreSQL must already be stopped. It neither stops nor starts them.
// The existing executor lock excludes deployment; a fresh journal cannot pass.
func (x *Executor) ColdBackup(ctx context.Context, expected backup.Binding, path string, key []byte) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.state.ActiveBackupID != "" {
		return ErrConflict
	}
	r, release, o, b, e := x.coldSource(ctx, expected)
	if e != nil {
		return e
	}
	return r.captureColdBackup(ctx, release, o, b, expected, path, key)
}

func (r *ComposeRunner) captureColdBackup(ctx context.Context, release Release, o Operation, b Bundle, expected backup.Binding, path string, key []byte) error {
	if !independentBackupKey(b, key) {
		return backup.ErrInvalid
	}
	db, e := r.coldContainers(ctx, release, o, b)
	if e != nil {
		return e
	}
	if r.coldDatabase(ctx, db, expected) != nil {
		return ErrUnconfirmed
	}
	w, e := backup.Create(path, expected, key)
	if e != nil {
		return e
	}
	defer w.Close()
	raw, _, e := readBundle(r.BundleRoot, release.ID, release.ComposeSHA256)
	if e != nil {
		return e
	}
	if e = w.Add("compose", func(out io.Writer) error { _, e := out.Write(raw); return e }); e != nil {
		return e
	}
	metadata, _ := json.Marshal(release)
	if e = w.Add("release", func(out io.Writer) error { _, e := out.Write(metadata); return e }); e != nil {
		return e
	}
	if e = w.Add("database", func(out io.Writer) error {
		return r.stream(ctx, nil, out, "exec", db, "pg_dump", "-U", "enterprise", "-d", "enterprise", "--format=custom", "--no-owner", "--no-acl")
	}); e != nil {
		return e
	}
	sources, _ := coldVolumeSources(b)
	for _, v := range coldVolumes {
		if e = r.unusedVolume(ctx, r.Project+"_"+sources[v]); e != nil {
			return e
		}
		if e = w.Add(v, func(out io.Writer) error {
			return r.volumeHelper(ctx, b.Services["enterprise-api"].Image, r.Project+"_"+sources[v], "export", nil, out)
		}); e != nil {
			return e
		}
	}
	// A supervisor restart during backup invalidates the attempt; no final
	// manifest is published. Trusted host operators must also honor maintenance.
	if _, e = r.coldContainers(ctx, release, o, b); e != nil {
		return e
	}
	if r.coldDatabase(ctx, db, expected) != nil {
		return ErrUnconfirmed
	}
	return w.Finalize()
}

func independentBackupKey(b Bundle, key []byte) bool {
	if len(key) != 32 {
		return false
	}
	for _, s := range b.Services {
		for _, value := range s.Environment {
			decoded, e := base64.RawURLEncoding.DecodeString(value)
			if e == nil && bytes.Equal(decoded, key) {
				return false
			}
		}
	}
	return true
}

func (x *Executor) coldSource(ctx context.Context, expected backup.Binding) (*ComposeRunner, Release, Operation, Bundle, error) {
	var release Release
	var o Operation
	var b Bundle
	r, ok := x.runner.(*ComposeRunner)
	if !ok || x.closed || x.poisoned || x.running || x.state.ActiveOperationID != "" || x.state.Generation != expected.Generation || x.state.CurrentReleaseID != expected.ReleaseID || x.state.CurrentReleaseDigest != expected.ReleaseDigest || x.state.ServerID != expected.ServerID || x.state.TenantID != expected.TenantID {
		return nil, release, o, b, ErrConflict
	}
	release = x.catalog[expected.ReleaseID]
	if !release.MatchesTarget(expected.TenantID, expected.ServerID) || release.SchemaVersion != expected.SchemaVersion {
		return nil, release, o, b, ErrConflict
	}
	for _, receipt := range x.state.Receipts {
		if receipt.State == "completed" && receipt.Generation == expected.Generation {
			o = receipt.Operation
			break
		}
	}
	_, b, e := r.bundle(release, o)
	if e != nil {
		return nil, release, o, b, e
	}
	if e = validateColdBundle(b, expected); e != nil {
		return nil, release, o, b, e
	}
	return r, release, o, b, nil
}

func validateColdBundle(b Bundle, expected backup.Binding) error {
	// Only the known nine-service enterprise layout is supported. Do not silently
	// omit an extra business service, volume or future schema.
	wanted := []string{"enterprise-api", "enterprise-db", "enterprise-gateway", "enterprise-im", "enterprise-livekit", "enterprise-media-init", "enterprise-minio", "enterprise-plugins", "enterprise-redis"}
	if len(b.Services) != len(wanted) || len(b.Volumes) != 6 || expected.Scope != "" || expected.SchemaVersion != 79 {
		return ErrBundle
	}
	for _, n := range wanted {
		if b.Services[n].Image == "" {
			return ErrBundle
		}
	}
	apiEnv := b.Services["enterprise-api"].Environment
	preview := apiEnv["IM_ENV"] == "development" && apiEnv["IM_TENANCY_PREVIEW"] == "true"
	production := apiEnv["IM_ENV"] == "production" && apiEnv["IM_TENANCY_PREVIEW"] == "false" && apiEnv["IM_TENANT_DEPLOYMENT_MODE"] == "dedicated_host"
	if (!preview && !production) || apiEnv["IM_TENANT_ID"] != expected.TenantID {
		return ErrBundle
	}
	if _, e := coldVolumeSources(b); e != nil {
		return e
	}
	for _, s := range b.Services {
		for _, v := range s.Volumes {
			if _, ok := b.Volumes[v.Source]; !ok {
				return ErrBundle
			}
		}
	}
	return nil
}

func (r *ComposeRunner) coldContainers(ctx context.Context, release Release, o Operation, b Bundle) (string, error) {
	items, e := r.ownedBackupContainers(ctx, release, o, b, true)
	if e != nil {
		return "", e
	}
	return items["enterprise-db"], nil
}

func (r *ComposeRunner) ownedBackupContainers(ctx context.Context, release Release, o Operation, b Bundle, cold bool) (map[string]string, error) {
	items, e := r.containers(ctx)
	if e != nil || len(items) != len(b.Services) {
		return nil, ErrUnconfirmed
	}
	seen := map[string]bool{}
	ids := map[string]string{}
	db := ""
	for _, c := range items {
		name := c.Labels["com.docker.compose.service"]
		s, ok := b.Services[name]
		if !ok || seen[name] || !r.owns(c.Labels) || c.Labels["io.frogim.release"] != release.ID || c.Labels["io.frogim.release-digest"] != release.Digest() || c.Labels["io.frogim.operation"] != o.ID || c.Image != s.Image {
			return nil, ErrUnconfirmed
		}
		seen[name] = true
		ids[name] = c.ID
		image, e := r.inspectImage(ctx, s.Image)
		if e != nil || image != c.ImageID {
			return nil, ErrUnconfirmed
		}
		if name == "enterprise-db" {
			if c.Status != "running" || c.Health != "healthy" {
				return nil, ErrUnconfirmed
			}
			db = c.ID
		} else if cold && (c.Status != "exited" || (c.ExitCode != 0 && c.ExitCode != 143)) {
			return nil, ErrUnconfirmed
		} else if !cold && c.Status != "running" && c.Status != "exited" {
			return nil, ErrUnconfirmed
		}
		// Labels are not enough: verify actual mounted named volumes too.
		out, e := r.command(ctx, nil, "inspect", "--format", "{{json .Mounts}}", c.ID)
		var mounts []struct {
			Type, Name, Destination string
			RW                      bool
		}
		if e != nil || json.Unmarshal(out, &mounts) != nil {
			return nil, ErrUnconfirmed
		}
		volumeCount := 0
		for _, m := range mounts {
			if m.Type == "tmpfs" {
				continue
			}
			if m.Type != "volume" {
				return nil, ErrUnconfirmed
			}
			volumeCount++
			found := false
			for _, v := range s.Volumes {
				if m.Name == r.Project+"_"+v.Source && m.Destination == v.Target && m.RW != v.ReadOnly {
					found = true
				}
			}
			if !found {
				return nil, ErrUnconfirmed
			}
		}
		if volumeCount != len(s.Volumes) {
			return nil, ErrUnconfirmed
		}
	}
	if r.resources(ctx, b) != nil {
		return nil, ErrUnconfirmed
	}
	// A second container mounting PostgreSQL data is not a valid cold source,
	// even if it lacks this project's labels.
	sources, e := coldVolumeSources(b)
	if e != nil {
		return nil, e
	}
	out, e := r.command(ctx, nil, "ps", "--filter", "volume="+r.Project+"_"+sources["postgres"], "--format", "{{.ID}}")
	users := strings.Fields(string(out))
	if e != nil || len(users) != 1 || !strings.HasPrefix(db, users[0]) {
		return nil, ErrUnconfirmed
	}
	return ids, nil
}
func (r *ComposeRunner) coldDatabase(ctx context.Context, db string, b backup.Binding) error {
	return r.suspendedDatabase(ctx, db, b, false)
}
func (r *ComposeRunner) suspendedDatabase(ctx context.Context, db string, b backup.Binding, allowClients bool) error {
	query := `SELECT json_build_object('tenant',tenant_id,'version',access_version,'disabled',NOT access_enabled,'confirmed',EXISTS(SELECT 1 FROM im_tenant_realm_operations o WHERE o.access_version=t.access_version AND NOT o.enabled AND o.state='completed'),'schema',(SELECT MAX(version) FROM im_schema_migrations),'clients',(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend' AND pid<>pg_backend_pid())) FROM im_tenant_identity t`
	out, e := r.command(ctx, nil, "exec", db, "psql", "-X", "-U", "enterprise", "-d", "enterprise", "-At", "-v", "ON_ERROR_STOP=1", "-c", query)
	var state struct {
		Tenant              string
		Version             int64
		Disabled, Confirmed bool
		Schema              int
		Clients             int
	}
	if e != nil || json.Unmarshal(bytes.TrimSpace(out), &state) != nil || state.Tenant != b.TenantID || state.Version != b.AccessVersion || !state.Disabled || !state.Confirmed || state.Schema != b.SchemaVersion || (!allowClients && state.Clients != 0) {
		return ErrUnconfirmed
	}
	return nil
}

// Logical dataset names stay stable after a staged restore changes physical
// volume names. All six datasets remain distinct and in this project.
func coldVolumeSources(b Bundle) (map[string]string, error) {
	targets := map[string][2]string{"postgres": {"enterprise-db", "/var/lib/postgresql/data"}, "redis": {"enterprise-redis", "/data"}, "media": {"enterprise-minio", "/data"}, "im": {"enterprise-im", "/data"}, "im-logs": {"enterprise-im", "/logs"}, "plugins": {"enterprise-plugins", "/plugins"}}
	result := map[string]string{}
	seen := map[string]bool{}
	for logical, target := range targets {
		for _, v := range b.Services[target[0]].Volumes {
			if v.Target == target[1] {
				if result[logical] != "" || seen[v.Source] || !serviceName.MatchString(v.Source) {
					return nil, ErrBundle
				}
				result[logical] = v.Source
				seen[v.Source] = true
			}
		}
		if result[logical] == "" {
			return nil, ErrBundle
		}
	}
	for name := range b.Volumes {
		if !seen[name] {
			return nil, ErrBundle
		}
	}
	if len(b.Volumes) != 6 {
		return nil, ErrBundle
	}
	return result, nil
}
func (r *ComposeRunner) unusedVolume(ctx context.Context, name string) error {
	out, e := r.command(ctx, nil, "volume", "inspect", "--format", "{{json .Labels}}", name)
	var labels map[string]string
	if e != nil || json.Unmarshal(out, &labels) != nil || !r.owns(labels) {
		return ErrUnconfirmed
	}
	out, e = r.command(ctx, nil, "ps", "--filter", "volume="+name, "--format", "{{.ID}}")
	if e != nil || len(bytes.TrimSpace(out)) != 0 {
		return ErrUnconfirmed
	}
	return nil
}
func (r *ComposeRunner) helperLabels(kind string) []string {
	return []string{"--label", "com.docker.compose.project=" + r.Project, "--label", "io.frogim.server=" + r.Server, "--label", "io.frogim.tenant=" + r.Tenant, "--label", "io.frogim.backup-helper=" + kind}
}

func (r *ComposeRunner) volumeHelper(ctx context.Context, image, volume, mode string, in io.Reader, out io.Writer) error {
	if !imageReference.MatchString(image) || !strings.HasPrefix(volume, r.Project+"_") || !serviceName.MatchString(strings.TrimPrefix(volume, r.Project+"_")) || (mode != "export" && mode != "import") {
		return ErrBundle
	}
	if r.unusedVolume(ctx, volume) != nil {
		return ErrUnconfirmed
	}
	mount := "type=volume,source=" + volume + ",target=/volume,volume-nocopy"
	caps := []string{"--cap-add", "DAC_OVERRIDE"}
	if mode == "export" {
		mount += ",readonly"
	} else {
		caps = append(caps, "--cap-add", "CHOWN", "--cap-add", "FOWNER")
	}
	args := append([]string{"create", "--pull=never", "--network=none", "--read-only", "--user=0:0", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--interactive", "--mount", mount}, caps...)
	args = append(args, r.helperLabels("volume-"+mode)...)
	if marker, ok := ctx.Value(backupHelperKey{}).(backupHelperMarker); ok && mode == "export" {
		args = append(args, "--label", "io.frogim.backup-job="+marker.Job, "--label", "io.frogim.backup-attempt="+marker.Attempt)
	}
	args = append(args, "--entrypoint", "/opt/frogim/tenant-volume", image, mode)
	data, e := r.command(ctx, nil, args...)
	id := strings.TrimSpace(string(data))
	if e != nil || !fingerprint.MatchString(id) {
		return ErrUnconfirmed
	}
	// Cleanup is restricted to this exact freshly created, owner-checked helper.
	defer r.removeHelper(id, "volume-"+mode)
	if r.stream(ctx, in, out, "start", "--attach", "--interactive", id) != nil {
		return ErrUnconfirmed
	}
	data, e = r.command(ctx, nil, "inspect", "--format", "{{.State.Status}}:{{.State.ExitCode}}", id)
	if e != nil || strings.TrimSpace(string(data)) != "exited:0" {
		return ErrUnconfirmed
	}
	return nil
}
