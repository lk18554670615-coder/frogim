package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

type composeBackupIO struct{ r *ComposeRunner }
type backupHelperKey struct{}
type backupHelperMarker struct{ Job, Attempt string }

func (c *composeBackupIO) bundle(p backupPlan) (Bundle, error) {
	_, b, e := c.r.bundle(p.Release, p.Deployment)
	if e != nil {
		return b, e
	}
	return b, validateColdBundle(b, p.Request.Binding)
}

// Volume existence and identity are frozen BEFORE any service is stopped.
// Recovery never runs compose up: it can only start the original container IDs,
// so a missing original volume cannot be replaced with a new empty one.
func (r *ComposeRunner) backupVolumeIdentity(ctx context.Context, b Bundle) (map[string]string, error) {
	sources, e := coldVolumeSources(b)
	if e != nil {
		return nil, e
	}
	result := map[string]string{}
	for logical, source := range sources {
		name := r.Project + "_" + source
		data, e := r.command(ctx, nil, "volume", "inspect", "--format", `{"name":{{json .Name}},"driver":{{json .Driver}},"createdAt":{{json .CreatedAt}},"scope":{{json .Scope}},"labels":{{json .Labels}},"options":{{json .Options}}}`, name)
		var v struct {
			Name, Driver, CreatedAt, Scope string
			Labels, Options                map[string]string
		}
		if e != nil || json.Unmarshal(data, &v) != nil || v.Name != name || v.Driver != "local" || v.Scope != "local" || v.CreatedAt == "" || len(v.Options) != 0 || !r.owns(v.Labels) {
			return nil, ErrUnconfirmed
		}
		raw, _ := json.Marshal(v)
		hash := sha256.Sum256(raw)
		result[logical] = hex.EncodeToString(hash[:])
	}
	return result, nil
}
func (c *composeBackupIO) source(ctx context.Context, p backupPlan, b Bundle, cold bool) (backupSource, error) {
	ids, e := c.r.ownedBackupContainers(ctx, p.Release, p.Deployment, b, cold)
	if e != nil {
		return backupSource{}, e
	}
	vols, e := c.r.backupVolumeIdentity(ctx, b)
	if e != nil {
		return backupSource{}, e
	}
	return backupSource{Containers: ids, Volumes: vols}, nil
}
func (c *composeBackupIO) match(ctx context.Context, p backupPlan, b Bundle, expected backupSource, cold bool) error {
	actual, e := c.source(ctx, p, b, cold)
	if e != nil || !expected.valid() || !maps.Equal(actual.Containers, expected.Containers) || !maps.Equal(actual.Volumes, expected.Volumes) {
		return ErrUnconfirmed
	}
	return nil
}
func (c *composeBackupIO) Prepare(ctx context.Context, p backupPlan, key []byte) (backupSource, error) {
	b, e := c.bundle(p)
	if e != nil {
		return backupSource{}, e
	}
	if !independentBackupKey(b, key) || c.r.Verify(ctx, p.Release, p.Deployment) != nil {
		return backupSource{}, ErrUnconfirmed
	}
	s, e := c.source(ctx, p, b, false)
	if e != nil {
		return s, e
	}
	if c.r.suspendedDatabase(ctx, s.Containers["enterprise-db"], p.Request.Binding, true) != nil {
		return backupSource{}, ErrUnconfirmed
	}
	return s, nil
}
func (c *composeBackupIO) Quiesce(ctx context.Context, p backupPlan, s backupSource) error {
	b, e := c.bundle(p)
	if e != nil {
		return e
	}
	if c.match(ctx, p, b, s, false) != nil || c.r.suspendedDatabase(ctx, s.Containers["enterprise-db"], p.Request.Binding, true) != nil {
		return ErrUnconfirmed
	}
	// Stop entry points first, then writers. PostgreSQL remains up only for the
	// transactionally consistent dump; coldDatabase requires no other clients.
	for _, name := range []string{"enterprise-gateway", "enterprise-api", "enterprise-im", "enterprise-livekit", "enterprise-media-init", "enterprise-minio", "enterprise-redis", "enterprise-plugins"} {
		id := s.Containers[name]
		if _, e = c.r.command(ctx, nil, "stop", "--time", "30", id); e != nil {
			return ErrUnconfirmed
		}
	}
	if c.match(ctx, p, b, s, true) != nil {
		return ErrUnconfirmed
	}
	return c.r.coldDatabase(ctx, s.Containers["enterprise-db"], p.Request.Binding)
}
func (c *composeBackupIO) Capture(ctx context.Context, p backupPlan, s backupSource, path string, key []byte) (ArchiveProof, error) {
	b, e := c.bundle(p)
	if e != nil {
		return ArchiveProof{}, e
	}
	if c.recoverHelpers(ctx, p, b) != nil || c.match(ctx, p, b, s, true) != nil || c.r.coldDatabase(ctx, s.Containers["enterprise-db"], p.Request.Binding) != nil {
		return ArchiveProof{}, ErrUnconfirmed
	}
	if info, e := os.Lstat(path); e == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ArchiveProof{}, ErrUnconfirmed
		}
		// Completed capture with lost journal ACK is authenticated, not overwritten.
		// Incomplete captures remain on disk and fail this attempt.
		return inspectArchive(path, p.Request.Binding, key)
	} else if !errors.Is(e, os.ErrNotExist) {
		return ArchiveProof{}, ErrUnconfirmed
	}
	marker := backupHelperMarker{p.Request.ID, strconv.Itoa(p.Attempt)}
	ctx = context.WithValue(ctx, backupHelperKey{}, marker)
	if e = c.r.captureColdBackup(ctx, p.Release, p.Deployment, b, p.Request.Binding, path, key); e != nil {
		return ArchiveProof{}, e
	}
	if c.match(ctx, p, b, s, true) != nil {
		return ArchiveProof{}, ErrUnconfirmed
	}
	return inspectArchive(path, p.Request.Binding, key)
}
func (c *composeBackupIO) Restore(ctx context.Context, p backupPlan, s backupSource) error {
	b, e := c.bundle(p)
	if e != nil {
		return e
	}
	if c.recoverHelpers(ctx, p, b) != nil || c.match(ctx, p, b, s, false) != nil {
		return ErrUnconfirmed
	}
	if c.r.suspendedDatabase(ctx, s.Containers["enterprise-db"], p.Request.Binding, true) != nil {
		return ErrUnconfirmed
	}
	// Start only existing IDs, in dependency order. No create/up/pull or volume
	// deletion is allowed in this recovery path, even after an interrupted stop.
	for _, name := range []string{"enterprise-db", "enterprise-plugins", "enterprise-redis", "enterprise-minio", "enterprise-media-init", "enterprise-im", "enterprise-livekit", "enterprise-api", "enterprise-gateway"} {
		if _, e = c.r.command(ctx, nil, "start", s.Containers[name]); e != nil {
			return ErrUnconfirmed
		}
		if e = c.waitHealthy(ctx, s.Containers[name]); e != nil {
			return e
		}
	}
	return nil
}
func (c *composeBackupIO) waitHealthy(ctx context.Context, id string) error {
	call, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	for {
		out, e := c.r.command(call, nil, "inspect", "--format", `{{.State.Status}}:{{with index .State "Health"}}{{.Status}}{{end}}`, id)
		state := strings.TrimSpace(string(out))
		if e != nil {
			return ErrUnconfirmed
		}
		if state == "running:healthy" {
			return nil
		}
		if !strings.HasPrefix(state, "running:") {
			return ErrUnconfirmed
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-call.Done():
			timer.Stop()
			return ErrUnconfirmed
		case <-timer.C:
		}
	}
}
func (c *composeBackupIO) Verify(ctx context.Context, p backupPlan, s backupSource) error {
	b, e := c.bundle(p)
	if e != nil {
		return e
	}
	if c.match(ctx, p, b, s, false) != nil || c.r.Verify(ctx, p.Release, p.Deployment) != nil {
		return ErrUnconfirmed
	}
	return c.r.suspendedDatabase(ctx, s.Containers["enterprise-db"], p.Request.Binding, true)
}

// An agent process may die while its Docker export container continues. Only
// this attempt's exact read-only, networkless helper can be removed. All foreign
// or changed resources fail closed; neither original containers nor volumes are
// deleted here. A privileged host operator is outside the agent trust boundary.
func (c *composeBackupIO) recoverHelpers(ctx context.Context, p backupPlan, b Bundle) error {
	out, e := c.r.command(ctx, nil, "ps", "--all", "--filter", "label=com.docker.compose.project="+c.r.Project, "--filter", "label=io.frogim.backup-job="+p.Request.ID, "--format", "{{.ID}}")
	if e != nil {
		return ErrUnconfirmed
	}
	ids := strings.Fields(string(out))
	if len(ids) > 5 {
		return ErrUnconfirmed
	}
	sources, e := coldVolumeSources(b)
	if e != nil {
		return e
	}
	volumes := map[string]bool{}
	for _, logical := range coldVolumes {
		volumes[c.r.Project+"_"+sources[logical]] = true
	}
	image := b.Services["enterprise-api"].Image
	for _, id := range ids {
		if len(id) < 12 || len(id) > 64 || strings.Trim(id, "abcdef0123456789") != "" {
			return ErrUnconfirmed
		}
		out, e = c.r.command(ctx, nil, "inspect", "--format", `{"id":{{json .Id}},"imageId":{{json .Image}},"image":{{json .Config.Image}},"labels":{{json .Config.Labels}},"entrypoint":{{json .Config.Entrypoint}},"cmd":{{json .Config.Cmd}},"user":{{json .Config.User}},"network":{{json .HostConfig.NetworkMode}},"readOnly":{{.HostConfig.ReadonlyRootfs}},"privileged":{{.HostConfig.Privileged}},"capAdd":{{json .HostConfig.CapAdd}},"capDrop":{{json .HostConfig.CapDrop}},"mounts":{{json .Mounts}}}`, id)
		var h struct {
			ID, ImageID, Image, User, Network string
			Labels                            map[string]string
			Entrypoint, Cmd, CapAdd, CapDrop  []string
			ReadOnly, Privileged              bool
			Mounts                            []struct {
				Type, Name, Destination string
				RW                      bool
			}
		}
		if e != nil || json.Unmarshal(out, &h) != nil || !fingerprint.MatchString(h.ID) || !strings.HasPrefix(h.ID, id) || !c.r.owns(h.Labels) || h.Labels["io.frogim.backup-helper"] != "volume-export" || h.Labels["io.frogim.backup-job"] != p.Request.ID || h.Labels["io.frogim.backup-attempt"] != strconv.Itoa(p.Attempt) || h.Image != image || !slices.Equal(h.Entrypoint, []string{"/opt/frogim/tenant-volume"}) || !slices.Equal(h.Cmd, []string{"export"}) || h.User != "0:0" || h.Network != "none" || !h.ReadOnly || h.Privileged || !singleCapability(h.CapAdd, "DAC_OVERRIDE") || !singleCapability(h.CapDrop, "ALL") || len(h.Mounts) != 1 {
			return ErrUnconfirmed
		}
		imageID, e := c.r.inspectImage(ctx, image)
		if e != nil || imageID != h.ImageID {
			return ErrUnconfirmed
		}
		m := h.Mounts[0]
		if m.Type != "volume" || !volumes[m.Name] || m.Destination != "/volume" || m.RW {
			return ErrUnconfirmed
		}
		if _, e = c.r.command(ctx, nil, "rm", "--force", h.ID); e != nil {
			return ErrUnconfirmed
		}
	}
	return nil
}

// Docker normalizes --cap-add=DAC_OVERRIDE to CAP_DAC_OVERRIDE in inspect.
// Accept only the equivalent spelling, never extra/duplicate capabilities.
func singleCapability(values []string, expected string) bool {
	return len(values) == 1 && strings.TrimPrefix(values[0], "CAP_") == expected
}
