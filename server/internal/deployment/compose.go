package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ComposeRunner controls only one fixed, project-scoped local Docker target.
// The current agent executable does not automatically enable this capability.
// Images must be preloaded; this runner never builds/pulls images, removes
// orphans/volumes, executes host shell text or changes a Docker context.
type ComposeRunner struct {
	Binary, Endpoint, BundleRoot, Project, Server, Tenant string
	Catalog                                               map[string]Release
	IsolationMode                                         string
}

func (r *ComposeRunner) valid() bool {
	if r.IsolationMode != "" && r.IsolationMode != "local_preview" && r.IsolationMode != "dedicated_host" {
		return false
	}
	if r.IsolationMode == "dedicated_host" && r.Endpoint != "unix:///var/run/docker.sock" {
		return false
	}
	return filepath.IsAbs(r.Binary) && filepath.IsAbs(r.BundleRoot) && (r.Endpoint == "unix:///var/run/docker.sock" || r.Endpoint == "npipe:////./pipe/dockerDesktopLinuxEngine") && r.Project == "frogim-deploy-"+r.Tenant && serviceName.MatchString(r.Project) && serviceName.MatchString(r.Server) && serviceName.MatchString(r.Tenant)
}

type boundedOutput struct{ bytes.Buffer }

func (w *boundedOutput) Write(b []byte) (int, error) {
	if w.Len()+len(b) > 1<<20 {
		return 0, ErrUnconfirmed
	}
	return w.Buffer.Write(b)
}
func (r *ComposeRunner) command(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	var out boundedOutput
	e := r.stream(ctx, bytes.NewReader(input), &out, args...)
	return out.Bytes(), e
}

// Binary dump/archive traffic must stream, not pass through bounded JSON output
// or PowerShell string pipelines. Stderr remains discarded to protect secrets.
func (r *ComposeRunner) stream(ctx context.Context, input io.Reader, output io.Writer, args ...string) error {
	if !r.valid() {
		return ErrBundle
	}
	info, e := os.Stat(r.Binary)
	if e != nil || !info.Mode().IsRegular() || strings.HasSuffix(strings.ToLower(r.Binary), ".cmd") || strings.HasSuffix(strings.ToLower(r.Binary), ".bat") {
		return ErrBundle
	}
	cmd := exec.CommandContext(ctx, r.Binary, append([]string{"--host", r.Endpoint}, args...)...)
	// A Compose plugin may outlive the parent CLI and retain stdout pipes.
	// Cancellation must still release the worker rather than hanging forever.
	cmd.WaitDelay = 2 * time.Second
	// Do not inherit DOCKER_CONTEXT/HOST, Compose overrides, proxy or arbitrary
	// loader variables. The executable and local engine are operator-configured.
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "ProgramFiles", "ProgramData", "ProgramW6432"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Stdin = input
	cmd.Stderr = io.Discard
	cmd.Stdout = output
	if cmd.Run() != nil {
		return ErrUnconfirmed
	}
	return nil
}

func (r *ComposeRunner) bundle(release Release, o Operation) ([]byte, Bundle, error) {
	var empty Bundle
	mode := r.IsolationMode
	if mode == "" {
		mode = "local_preview"
	}
	if release.isolationMode() != mode {
		return nil, empty, ErrBundle
	}
	if !r.valid() || o.ServerID != r.Server || o.TenantID != r.Tenant || release.ID != o.ReleaseID || release.Digest() != o.ReleaseDigest || r.Catalog[release.ID].Digest() != release.Digest() || (release.TenantID != "" && !release.MatchesTarget(r.Tenant, r.Server)) {
		return nil, empty, ErrBundle
	}
	_, b, e := readBundle(r.BundleRoot, release.ID, release.ComposeSHA256)
	if e != nil || validateBundle(b, release) != nil {
		return nil, empty, ErrBundle
	}
	var data map[string]any
	raw, _ := json.Marshal(b)
	_ = json.Unmarshal(raw, &data)
	owner := map[string]string{"io.frogim.server": r.Server, "io.frogim.tenant": r.Tenant}
	for _, section := range []string{"networks", "volumes"} {
		if resources, ok := data[section].(map[string]any); ok {
			for _, resource := range resources {
				resource.(map[string]any)["labels"] = owner
			}
		}
	}
	for _, service := range data["services"].(map[string]any) {
		service.(map[string]any)["labels"] = map[string]string{"io.frogim.server": r.Server, "io.frogim.tenant": r.Tenant, "io.frogim.release": release.ID, "io.frogim.release-digest": release.Digest(), "io.frogim.operation": o.ID}
	}
	// Compose interpolation must not reinterpret passwords or container-side
	// environment references using the host environment or a .env file.
	var escape func(any) any
	escape = func(v any) any {
		switch value := v.(type) {
		case string:
			return strings.ReplaceAll(value, "$", "$$")
		case map[string]any:
			for key, item := range value {
				value[key] = escape(item)
			}
		case []any:
			for n, item := range value {
				value[n] = escape(item)
			}
		}
		return v
	}
	raw, e = json.Marshal(escape(data))
	return raw, b, e
}
func (r *ComposeRunner) compose(ctx context.Context, raw []byte, args ...string) ([]byte, error) {
	base := []string{"compose", "--project-name", r.Project, "--project-directory", r.BundleRoot, "--env-file", os.DevNull, "--file", "-"}
	return r.command(ctx, raw, append(base, args...)...)
}

type containerState struct {
	ID       string            `json:"id"`
	ImageID  string            `json:"imageId"`
	Image    string            `json:"image"`
	Labels   map[string]string `json:"labels"`
	Status   string            `json:"status"`
	Health   string            `json:"health"`
	ExitCode int               `json:"exitCode"`
}

func (r *ComposeRunner) containers(ctx context.Context) ([]containerState, error) {
	out, e := r.command(ctx, nil, "ps", "--all", "--filter", "label=com.docker.compose.project="+r.Project, "--format", "{{.ID}}")
	if e != nil {
		return nil, e
	}
	ids := strings.Fields(string(out))
	if len(ids) > 20 {
		return nil, ErrUnconfirmed
	}
	result := make([]containerState, 0, len(ids))
	for _, id := range ids {
		if !regexp.MustCompile(`^[a-f0-9]{12,64}$`).MatchString(id) {
			return nil, ErrUnconfirmed
		}
		out, e = r.command(ctx, nil, "inspect", "--format", `{"id":{{json .Id}},"imageId":{{json .Image}},"image":{{json .Config.Image}},"labels":{{json .Config.Labels}},"status":{{json .State.Status}},"exitCode":{{.State.ExitCode}},"health":{{with index .State "Health"}}{{json .Status}}{{else}}""{{end}}}`, id)
		var item containerState
		if e != nil || json.Unmarshal(out, &item) != nil {
			return nil, ErrUnconfirmed
		}
		result = append(result, item)
	}
	return result, nil
}
func (r *ComposeRunner) owns(labels map[string]string) bool {
	return labels["com.docker.compose.project"] == r.Project && labels["io.frogim.server"] == r.Server && labels["io.frogim.tenant"] == r.Tenant
}
func (r *ComposeRunner) resources(ctx context.Context, b Bundle) error {
	for kind, names := range map[string]map[string]bool{"volume": {}, "network": {}} {
		if kind == "volume" {
			for n := range b.Volumes {
				names[r.Project+"_"+n] = true
			}
		} else {
			for n := range b.Networks {
				names[r.Project+"_"+n] = true
			}
		}
		out, e := r.command(ctx, nil, kind, "ls", "--format", "{{.Name}}")
		if e != nil {
			return e
		}
		for _, name := range strings.Fields(string(out)) {
			if !names[name] {
				continue
			}
			out, e = r.command(ctx, nil, kind, "inspect", "--format", "{{json .Labels}}", name)
			var labels map[string]string
			if e != nil || json.Unmarshal(out, &labels) != nil || !r.owns(labels) {
				return ErrUnconfirmed
			}
		}
	}
	return nil
}
func (r *ComposeRunner) inspectImage(ctx context.Context, reference string) (string, error) {
	out, e := r.command(ctx, nil, "image", "inspect", "--format", "{{.Id}}", reference)
	id := strings.TrimSpace(string(out))
	if e != nil || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(id) {
		return "", ErrUnconfirmed
	}
	return id, nil
}

func (r *ComposeRunner) Apply(ctx context.Context, release Release, o Operation) error {
	raw, b, e := r.bundle(release, o)
	if e != nil {
		return e
	}
	if e = r.resources(ctx, b); e != nil {
		return e
	}
	current, e := r.containers(ctx)
	if e != nil {
		return e
	}
	for _, c := range current {
		service := c.Labels["com.docker.compose.service"]
		if !r.owns(c.Labels) || b.Services[service].Image == "" {
			return ErrUnconfirmed
		}
		// Partial retries may contain only the frozen prior release and this
		// exact operation. Foreign drift cannot be silently adopted/overwritten.
		id := c.Labels["io.frogim.release"]
		image := b.Services[service].Image
		if id == release.ID && c.Labels["io.frogim.operation"] == o.ID {
			if c.Labels["io.frogim.release-digest"] != release.Digest() {
				return ErrUnconfirmed
			}
		} else {
			prior, ok := r.Catalog[o.ExpectedReleaseID]
			if !ok || id != prior.ID || c.Labels["io.frogim.release-digest"] != prior.Digest() {
				return ErrUnconfirmed
			}
			_, previous, e := readBundle(r.BundleRoot, prior.ID, prior.ComposeSHA256)
			if e != nil || validateBundle(previous, prior) != nil {
				return ErrBundle
			}
			image = previous.Services[service].Image
		}
		imageID, e := r.inspectImage(ctx, image)
		if image == "" || e != nil || c.Image != image || c.ImageID != imageID {
			return ErrUnconfirmed
		}
	}
	for _, s := range b.Services {
		if _, e = r.inspectImage(ctx, s.Image); e != nil {
			return e
		}
	}
	_, e = r.compose(ctx, raw, "up", "--detach", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "120")
	return e
}
func (r *ComposeRunner) Verify(ctx context.Context, release Release, o Operation) error {
	_, b, e := r.bundle(release, o)
	if e != nil {
		return e
	}
	current, e := r.containers(ctx)
	if e != nil || len(current) != len(b.Services) {
		return ErrUnconfirmed
	}
	seen := map[string]bool{}
	for _, c := range current {
		name := c.Labels["com.docker.compose.service"]
		service, ok := b.Services[name]
		if !ok || seen[name] || !r.owns(c.Labels) || c.Status != "running" || c.Health != "healthy" || c.Labels["io.frogim.release"] != release.ID || c.Labels["io.frogim.release-digest"] != release.Digest() || c.Labels["io.frogim.operation"] != o.ID || c.Image != service.Image {
			return ErrUnconfirmed
		}
		seen[name] = true
		image, e := r.inspectImage(ctx, service.Image)
		if e != nil || image != c.ImageID {
			return ErrUnconfirmed
		}
	}
	return r.resources(ctx, b)
}

func catalogList(c map[string]Release) []Release {
	list := make([]Release, 0, len(c))
	for _, r := range c {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}
