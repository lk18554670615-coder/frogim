package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/privatefile"
)

// StageColdRestore never changes the active release, original volumes or realm.
// It authenticates the complete set, restores into NEW volumes, validates the
// restored DB identity/schema/suspension, and finally publishes private staged
// release metadata. Activation is a separate, platform-authorized deployment.
// This first core requires the original confirmed executor journal/current
// release; it is not lost-host disaster adoption or cross-server migration.
func (x *Executor) StageColdRestore(ctx context.Context, expected backup.Binding, path string, key []byte, id string, sequence int64) (Release, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.state.ActiveBackupID != "" {
		return Release{}, ErrConflict
	}
	fail := func() (Release, error) { return Release{}, ErrUnconfirmed }
	r, prior, o, b, e := x.coldSource(ctx, expected)
	if e != nil {
		return Release{}, e
	}
	if !independentBackupKey(b, key) {
		return fail()
	}
	db, e := r.coldContainers(ctx, prior, o, b)
	if e != nil || r.coldDatabase(ctx, db, expected) != nil {
		return fail()
	}
	if !serviceName.MatchString(id) || len(id) > 24 || id == prior.ID {
		return fail()
	}
	for _, release := range r.Catalog {
		if release.ID == id || release.Sequence >= sequence {
			return fail()
		}
	}
	archive, e := backup.Open(path, expected, key)
	if e != nil {
		return fail()
	}
	defer archive.Close()
	want := append(slices.Clone(coldVolumes), "database", "compose", "release")
	got := []string{}
	for _, f := range archive.Files() {
		got = append(got, f.Name)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		return fail()
	}
	var raw, metadata boundedOutput
	if archive.Read("compose", &raw) != nil || archive.Read("release", &metadata) != nil {
		return fail()
	}
	original, _, e := readBundle(r.BundleRoot, prior.ID, prior.ComposeSHA256)
	if e != nil || !bytes.Equal(original, raw.Bytes()) {
		return fail()
	}
	var stored Release
	if json.Unmarshal(metadata.Bytes(), &stored) != nil || stored.Digest() != prior.Digest() {
		return fail()
	}
	// Parse all volume archives before creating any destination resource.
	for _, v := range coldVolumes {
		if consumeArchive(archive, v, func(in io.Reader) error { return backup.ReadVolume(in, "") }) != nil {
			return fail()
		}
	}
	volumes := map[string]string{}
	newVolumes := map[string]LocalVolume{}
	sources, _ := coldVolumeSources(b)
	for name, source := range sources {
		replacement := "restore-" + id + "-" + name
		if !serviceName.MatchString(replacement) {
			return fail()
		}
		volumes[source] = replacement
		newVolumes[replacement] = LocalVolume{}
	}
	all, e := r.command(ctx, nil, "volume", "ls", "--format", "{{.Name}}")
	if e != nil {
		return fail()
	}
	for _, name := range strings.Fields(string(all)) {
		for _, replacement := range volumes {
			if name == r.Project+"_"+replacement {
				return fail()
			}
		}
	}
	// Reserve the operation before creating volumes. Failed/partial staging stays
	// visible and cannot be silently overwritten or mistaken for completion.
	dir := filepath.Join(r.BundleRoot, id)
	if os.Mkdir(dir, 0700) != nil {
		return fail()
	}
	intent, _ := json.Marshal(struct {
		Binding backup.Binding    `json:"binding"`
		Volumes map[string]string `json:"volumes"`
		State   string            `json:"state"`
	}{expected, volumes, "staging-not-activated"})
	if writePrivate(filepath.Join(dir, "restore-intent.json"), intent) != nil {
		return fail()
	}
	for _, name := range volumes {
		args := []string{"volume", "create", "--driver=local", "--label", "com.docker.compose.project=" + r.Project, "--label", "com.docker.compose.volume=" + name, "--label", "io.frogim.server=" + r.Server, "--label", "io.frogim.tenant=" + r.Tenant, "--label", "io.frogim.restore=" + id, r.Project + "_" + name}
		if _, e = r.command(ctx, nil, args...); e != nil {
			return fail()
		}
	}
	for _, v := range coldVolumes {
		if consumeArchive(archive, v, func(in io.Reader) error {
			return r.volumeHelper(ctx, b.Services["enterprise-api"].Image, r.Project+"_"+volumes[sources[v]], "import", in, io.Discard)
		}) != nil {
			return fail()
		}
	}
	if r.restoreDatabase(ctx, b, archive, expected, r.Project+"_"+volumes[sources["postgres"]]) != nil {
		return fail()
	}
	if _, e = r.coldContainers(ctx, prior, o, b); e != nil || r.coldDatabase(ctx, db, expected) != nil {
		return fail()
	}
	for _, v := range volumes {
		if r.unusedVolume(ctx, r.Project+"_"+v) != nil {
			return fail()
		}
	}
	for name, s := range b.Services {
		for n := range s.Volumes {
			s.Volumes[n].Source = volumes[s.Volumes[n].Source]
		}
		b.Services[name] = s
	}
	b.Volumes = newVolumes
	release := restoredRelease(prior, id, sequence, r.Tenant, r.Server)
	encoded, e := json.MarshalIndent(b, "", "  ")
	if e != nil {
		return fail()
	}
	sum := sha256.Sum256(encoded)
	release.ComposeSHA256 = hex.EncodeToString(sum[:])
	if !release.Valid() || validateBundle(b, release) != nil {
		return fail()
	}
	metadataBytes, _ := json.MarshalIndent(release, "", "  ")
	if writePrivate(filepath.Join(dir, "compose.json"), encoded) != nil || writePrivate(filepath.Join(dir, "release.json"), metadataBytes) != nil {
		return fail()
	}
	return release, nil
}

func restoredRelease(prior Release, id string, sequence int64, tenant, server string) Release {
	// Restoring changes volumes, not the isolation profile. Do not copy old
	// rollback targets: they refer to the prior data generation.
	return Release{ID: id, Sequence: sequence, Runtime: prior.Runtime, SchemaVersion: prior.SchemaVersion, TenantID: tenant, ServerID: server, IsolationMode: prior.IsolationMode}
}

func writePrivate(path string, data []byte) error {
	f, e := privatefile.Create(path)
	if e != nil {
		return ErrUnconfirmed
	}
	defer f.Close()
	if n, e := f.Write(data); e != nil || n != len(data) || f.Sync() != nil {
		return ErrUnconfirmed
	}
	return nil
}
func consumeArchive(r *backup.Reader, name string, consume func(io.Reader) error) error {
	input, output := io.Pipe()
	done := make(chan error, 1)
	go func() { e := r.Read(name, output); _ = output.CloseWithError(e); done <- e }()
	e := consume(input)
	_ = input.CloseWithError(e)
	other := <-done
	if e != nil || other != nil {
		return ErrUnconfirmed
	}
	return nil
}
func (r *ComposeRunner) removeHelper(id, kind string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !fingerprint.MatchString(id) {
		return false
	}
	out, e := r.command(ctx, nil, "inspect", "--format", "{{json .Config.Labels}}", id)
	var labels map[string]string
	if e != nil || json.Unmarshal(out, &labels) != nil || !r.owns(labels) || labels["io.frogim.backup-helper"] != kind {
		return false
	}
	_, e = r.command(ctx, nil, "rm", "--force", id)
	return e == nil
}

func (r *ComposeRunner) restoreDatabase(ctx context.Context, b Bundle, archive *backup.Reader, expected backup.Binding, volume string) error {
	if r.unusedVolume(ctx, volume) != nil {
		return ErrUnconfirmed
	}
	password := b.Services["enterprise-db"].Environment["POSTGRES_PASSWORD"]
	if len(password) != 43 || strings.ContainsAny(password, "\r\n") {
		return ErrBundle
	}
	// No secret in command arguments. The env file is private and short lived;
	// unlike POSTGRES_HOST_AUTH_METHOD=trust this preserves password protection.
	dir, e := os.MkdirTemp("", "frogim-restore-private-")
	if e != nil {
		return ErrUnconfirmed
	}
	path := filepath.Join(dir, "postgres.env")
	defer os.Remove(dir)
	defer os.Remove(path)
	if writePrivate(path, []byte("POSTGRES_USER=enterprise\nPOSTGRES_DB=enterprise\nPOSTGRES_PASSWORD="+password+"\n")) != nil {
		return ErrUnconfirmed
	}
	args := []string{"create", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--cap-add=CHOWN", "--cap-add=DAC_OVERRIDE", "--cap-add=FOWNER", "--cap-add=SETUID", "--cap-add=SETGID", "--security-opt=no-new-privileges", "--tmpfs=/tmp", "--tmpfs=/var/run/postgresql", "--env-file", path, "--mount", "type=volume,source=" + volume + ",target=/var/lib/postgresql/data,volume-nocopy"}
	args = append(args, r.helperLabels("postgres-restore")...)
	args = append(args, b.Services["enterprise-db"].Image)
	data, e := r.command(ctx, nil, args...)
	id := strings.TrimSpace(string(data))
	if e != nil || !fingerprint.MatchString(id) {
		return ErrUnconfirmed
	}
	defer r.removeHelper(id, "postgres-restore")
	if _, e = r.command(ctx, nil, "start", id); e != nil {
		return e
	}
	ready, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		// The temporary initdb server accepts sockets before initialization is
		// complete. Only the final server listens on TCP; nothing is published.
		if _, e = r.command(ready, nil, "exec", id, "pg_isready", "-h", "127.0.0.1", "-U", "enterprise", "-d", "enterprise"); e == nil {
			break
		}
		select {
		case <-ready.Done():
			return ErrUnconfirmed
		case <-ticker.C:
		}
	}
	e = consumeArchive(archive, "database", func(in io.Reader) error {
		return r.stream(ctx, in, io.Discard, "exec", "-i", id, "pg_restore", "-U", "enterprise", "-d", "enterprise", "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl")
	})
	if e != nil || r.coldDatabase(ctx, id, expected) != nil {
		return ErrUnconfirmed
	}
	if _, e = r.command(ctx, nil, "stop", "--time=30", id); e != nil {
		return e
	}
	data, e = r.command(ctx, nil, "inspect", "--format", "{{.State.Status}}:{{.State.ExitCode}}", id)
	if e != nil || strings.TrimSpace(string(data)) != "exited:0" {
		return ErrUnconfirmed
	}
	return nil
}
