package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Runtime files only exist on a container-local tmpfs. Each role receives its
// own files, never the platform private key or another role's credentials.
const RuntimeFilesEnvironment = "FROGIM_RUNTIME_FILES"

var runtimeFiles = map[string][]string{
	"api":              {"ca.pem", "control.pem", "control.key"},
	"gateway":          {"public.pem", "public.key", "Caddyfile"},
	"im":               {"wk.yaml"},
	"livekit":          {"livekit.yaml"},
	"platform":         {"ca.pem", "control.pem", "control.key", "peers.json", "agents.json", "catalog.json", "apns.pem"},
	"platform-gateway": {"public.pem", "public.key", "Caddyfile"},
}

func PrepareRuntime(role, root, encoded string) error {
	names, ok := runtimeFiles[role]
	if !ok || !filepath.IsAbs(root) || len(encoded) > 256<<10 {
		return ErrBundle
	}
	d := json.NewDecoder(strings.NewReader(encoded))
	d.DisallowUnknownFields()
	var files map[string]string
	if d.Decode(&files) != nil || d.Decode(new(any)) != io.EOF || len(files) != len(names) {
		return ErrBundle
	}
	for _, name := range names {
		if files[name] == "" || len(files[name]) > 64<<10 {
			return ErrBundle
		}
	}
	bounded, e := os.OpenRoot(root)
	if e != nil {
		return ErrBundle
	}
	defer bounded.Close()
	for _, name := range names {
		// A fresh container gets a fresh tmpfs. Re-running the helper must not
		// overwrite files or follow symlinks planted by a running child.
		f, e := bounded.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return ErrBundle
		}
		_, e = io.Copy(f, strings.NewReader(files[name]))
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			return ErrBundle
		}
	}
	return nil
}

func RuntimeCommand(role string) (string, []string, error) {
	switch role {
	case "platform":
		return "/opt/frogim/platform", nil, nil
	case "api":
		return "/opt/frogim/enterprise", nil, nil
	case "gateway", "platform-gateway":
		return "/usr/bin/caddy", []string{"run", "--config", "/config/Caddyfile", "--adapter", "caddyfile"}, nil
	case "im":
		return "/opt/frogim/wukongim", []string{"--config=/config/wk.yaml", "--ignoreMissingConfig=false"}, nil
	case "livekit":
		return "/opt/frogim/livekit", []string{"--config", "/config/livekit.yaml"}, nil
	}
	return "", nil, ErrBundle
}

// A long-running seed service gives Compose a real health dependency. Docker's
// named-volume copy-up initializes the image-owned /plugins directory; no root
// shell/chown or writable host path is necessary at deployment time.
func CheckPluginSeed(root string) error {
	const name = "wk.plugin.im-policy-linux-amd64.wkp"
	checksum, e := os.ReadFile("/opt/frogim/policy.sha256")
	if e != nil || len(checksum) != 64 {
		return ErrBundle
	}
	f, e := os.Open(filepath.Join(root, name))
	if e != nil {
		return ErrBundle
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, io.LimitReader(f, 64<<20)); e != nil || !bytes.Equal([]byte(hex.EncodeToString(h.Sum(nil))), checksum) {
		return ErrBundle
	}
	return nil
}

func RuntimeHealth(ctx context.Context, role string) error {
	if role == "plugins" {
		return CheckPluginSeed("/plugins")
	}
	endpoints := map[string]string{"api": "http://127.0.0.1:8080/ready", "gateway": "http://127.0.0.1:8080/health", "im": "http://127.0.0.1:5001/health", "livekit": "http://127.0.0.1:7880/", "platform": "http://127.0.0.1:8090/health", "platform-gateway": "http://127.0.0.1:8080/health"}
	endpoint, ok := endpoints[role]
	if !ok {
		return ErrBundle
	}
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return ErrBundle
	}
	if role == "im" {
		req.Header.Set("token", os.Getenv("WK_MANAGERTOKEN"))
	}
	c := http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer c.CloseIdleConnections()
	r, e := c.Do(req)
	if e != nil {
		return errors.New("runtime health unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return errors.New("runtime health unavailable")
	}
	return nil
}
