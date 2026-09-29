package backup

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

func TestOffsiteMinioTLSAndCredentialIsolation(t *testing.T) {
	if os.Getenv("TENANCY_OFFSITE_DOCKER_TEST") != "local" {
		t.Skip("explicit isolated MinIO Docker fixture not enabled")
	}
	const minioImage = "minio/minio@sha256:d249d1fb6966de4d8ad26c04754b545205ff15a62e4fd19ebd0f26fa5baacbc0"
	const mcImage = "minio/mc@sha256:fb8f773eac8ef9d6da0486d5dec2f42f219358bcb8de579d1623d518c9ebd4cc"
	binary, e := exec.LookPath("docker")
	if e != nil {
		t.Fatal("Docker unavailable")
	}
	endpoint := "unix:///var/run/docker.sock"
	if runtime.GOOS == "windows" {
		endpoint = "npipe:////./pipe/dockerDesktopLinuxEngine"
	}
	docker := func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, binary, append([]string{"--host", endpoint}, args...)...)
		cmd.WaitDelay = 2 * time.Second
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = io.Discard
		e := cmd.Run()
		return bytes.TrimSpace(out.Bytes()), e
	}
	for _, image := range []string{minioImage, mcImage} {
		if _, e = docker(t.Context(), "image", "inspect", image); e != nil {
			t.Fatal("required pinned image not preloaded; never pulls")
		}
	}
	secret := func() string {
		var raw [24]byte
		if _, e := rand.Read(raw[:]); e != nil {
			t.Fatal(e)
		}
		return hex.EncodeToString(raw[:])
	}
	fixture := "offsite-" + secret()[:12]
	root := t.TempDir()
	certDir := filepath.Join(root, "certs")
	mcDir := filepath.Join(root, "mc")
	for _, dir := range []string{certDir, filepath.Join(mcDir, "certs", "CAs")} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(35), Subject: pkix.Name{CommonName: "offsite local fixture"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"host.docker.internal"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if e != nil {
		t.Fatal(e)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	priv, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		t.Fatal(e)
	}
	write := func(path string, data []byte) {
		t.Helper()
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal("fixture file")
		}
	}
	write(filepath.Join(certDir, "public.crt"), cert)
	write(filepath.Join(certDir, "private.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}))
	write(filepath.Join(mcDir, "certs", "CAs", "fixture.crt"), cert)
	rootAccess, rootSecret := "root"+secret()[:12], secret()
	envPath := filepath.Join(root, "minio.env")
	write(envPath, []byte("MINIO_ROOT_USER="+rootAccess+"\nMINIO_ROOT_PASSWORD="+rootSecret+"\nMINIO_BROWSER=off\n"))
	id, e := docker(t.Context(), "create", "--pull=never", "--name", fixture, "--label", "io.frogim.offsite-test="+fixture, "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=768m", "--tmpfs=/data:rw,size=268435456", "--tmpfs=/tmp", "--env-file", envPath, "--mount", "type=bind,source="+certDir+",target=/certs,readonly", "--publish", "127.0.0.1::9000", minioImage, "server", "--address", ":9000", "--certs-dir", "/certs", "/data")
	container := string(id)
	if e != nil || !digest.MatchString(container) {
		t.Fatal("isolated MinIO creation failed")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		data, e := docker(ctx, "inspect", "--format", "{{json .Config.Labels}}", container)
		var labels map[string]string
		if e != nil || json.Unmarshal(data, &labels) != nil || labels["io.frogim.offsite-test"] != fixture {
			t.Error("refusing unverified test-container cleanup")
			return
		}
		if _, e = docker(ctx, "rm", "--force", container); e != nil {
			t.Error("test MinIO cleanup failed")
		}
	})
	if _, e = docker(t.Context(), "start", container); e != nil {
		t.Fatal("MinIO start")
	}
	portData, e := docker(t.Context(), "inspect", "--format", `{{(index (index .NetworkSettings.Ports "9000/tcp") 0).HostPort}}`, container)
	if e != nil {
		t.Fatal("MinIO port")
	}
	port := string(portData)
	c, b, path, key := offsiteFixture(t)
	c.Endpoint = "https://127.0.0.1:" + port
	c.CAFile = filepath.Join(certDir, "public.crt")
	adminConfig := c
	adminConfig.AccessKey = rootAccess
	adminConfig.SecretKey = rootSecret
	admin, e := NewOffsite(adminConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	adminStore := admin.objects.(*s3Objects)
	deadline := time.Now().Add(30 * time.Second)
	for {
		e = adminStore.client.MakeBucket(t.Context(), c.Bucket, minio.MakeBucketOptions{Region: c.Region})
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("MinIO TLS ready/bucket initialization failed")
		}
		time.Sleep(150 * time.Millisecond)
	}
	configs := []OffsiteConfig{c, c, c}
	configs[1].ID = "beta-offsite"
	configs[1].TenantID = "beta"
	configs[1].ServerID = "host-beta"
	configs[2].ID = "platform-offsite"
	configs[2].Scope = "platform"
	configs[2].TenantID = ""
	configs[2].ServerID = ""
	configs[2].DirectoryID = "9a161c47-a687-415d-94ab-908cbd52b7a3"
	var mcEnv strings.Builder
	for n := range configs {
		configs[n].AccessKey = "user" + secret()[:12]
		configs[n].SecretKey = secret()
		policy, e := configs[n].IAMPolicy()
		if e != nil {
			t.Fatal(e)
		}
		write(filepath.Join(mcDir, fmt.Sprintf("policy%d.json", n)), policy)
		fmt.Fprintf(&mcEnv, "KEY%d=%s\nSECRET%d=%s\n", n, configs[n].AccessKey, n, configs[n].SecretKey)
	}
	write(filepath.Join(root, "mc.env"), []byte(mcEnv.String()))
	configJSON, _ := json.Marshal(map[string]any{"version": "10", "aliases": map[string]any{"fixture": map[string]any{"url": "https://host.docker.internal:" + port, "accessKey": rootAccess, "secretKey": rootSecret, "api": "S3v4", "path": "on"}}})
	write(filepath.Join(mcDir, "config.json"), configJSON)
	// This fixed shell runs INSIDE a disposable Linux helper, not PowerShell.
	// Secrets are in the private fixture env file, never Docker command args.
	script := `mc --config-dir /config admin user add fixture "$KEY0" "$SECRET0" && mc --config-dir /config admin policy create fixture scoped-alpha /config/policy0.json && mc --config-dir /config admin policy attach fixture scoped-alpha --user "$KEY0" && mc --config-dir /config admin user add fixture "$KEY1" "$SECRET1" && mc --config-dir /config admin policy create fixture scoped-beta /config/policy1.json && mc --config-dir /config admin policy attach fixture scoped-beta --user "$KEY1" && mc --config-dir /config admin user add fixture "$KEY2" "$SECRET2" && mc --config-dir /config admin policy create fixture scoped-platform /config/policy2.json && mc --config-dir /config admin policy attach fixture scoped-platform --user "$KEY2"`
	if _, e = docker(t.Context(), "run", "--rm", "--pull=never", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--tmpfs=/tmp", "--add-host", "host.docker.internal:host-gateway", "--env-file", filepath.Join(root, "mc.env"), "--mount", "type=bind,source="+mcDir+",target=/config", "--entrypoint", "/bin/sh", mcImage, "-ec", script); e != nil {
		t.Fatal("fixture scoped user provisioning failed")
	}
	for _, config := range configs {
		if e := adminStore.putNew(t.Context(), config.objectRoot()+"/isolation-sentinel", []byte("known-existing-fixture")); e != nil {
			t.Fatal("sentinel fixture")
		}
	}
	for n, config := range configs {
		o, e := NewOffsite(config)
		if e != nil {
			t.Fatal(e)
		}
		defer o.Close()
		binding := b
		if n == 1 {
			binding.TenantID = "beta"
			binding.ServerID = "host-beta"
		} else if n == 2 {
			binding = Binding{Scope: "platform", DirectoryID: config.DirectoryID, BackupID: "backup-one", ReleaseID: "platform-one", ReleaseDigest: strings.Repeat("c", 64), SchemaVersion: 17}
		}
		source := path
		if n > 0 {
			source = filepath.Join(t.TempDir(), "archive")
			w, e := Create(source, binding, key)
			if e != nil {
				t.Fatal(e)
			}
			if w.Add("database", func(out io.Writer) error { _, e := io.WriteString(out, "fixture"+fmt.Sprint(n)); return e }) != nil || w.Finalize() != nil {
				t.Fatal("fixture archive")
			}
			w.Close()
		}
		d, e := o.Deliver(t.Context(), binding, source, key)
		if e != nil {
			t.Fatal("scoped upload", n, e)
		}
		if repeated, e := o.Deliver(t.Context(), binding, source, key); e != nil || repeated != d {
			t.Fatal("real upload idempotency")
		}
		if e = o.Retrieve(t.Context(), binding, d, filepath.Join(t.TempDir(), "download"), key); e != nil {
			t.Fatal("scoped download", e)
		}
		if n == 2 {
			const date = "2026-09-29"
			if e = o.PublishDailyReceipt(t.Context(), date, d, key); e != nil {
				t.Fatal("daily index", e)
			}
			if e = o.PublishDailyReceipt(t.Context(), date, d, key); e != nil {
				t.Fatal("daily index repeat", e)
			}
			found, e := o.ReadDailyReceipt(t.Context(), date, key)
			if e != nil || found.Delivery != d {
				t.Fatal("daily index discovery", e)
			}
			if cli := os.Getenv("TENANCY_OFFSITE_CLI"); cli != "" {
				operator := t.TempDir()
				keyFile := filepath.Join(operator, "key.bin")
				write(keyFile, key)
				journal := filepath.Join(operator, "discovery-journal")
				cfgFile := filepath.Join(operator, "discover.json")
				cfg, _ := json.Marshal(map[string]any{"destination": config, "journalDirectory": journal, "dailyDate": date, "actor": "fixture-operator", "reason": "lost-host receipt discovery test"})
				write(cfgFile, cfg)
				for range 2 {
					cmd := exec.CommandContext(t.Context(), cli, "-mode", "discover-daily", "-config", cfgFile, "-key-file", keyFile, "-confirmed")
					cmd.Stderr = io.Discard
					out, e := cmd.Output()
					if e != nil || !bytes.Contains(out, []byte("Authenticated daily receipt saved")) {
						t.Fatal("daily CLI discovery")
					}
				}
				var discovered Delivery
				raw, e := os.ReadFile(filepath.Join(journal, "delivery.json"))
				if e != nil || json.Unmarshal(raw, &discovered) != nil || discovered != d {
					t.Fatal("daily CLI receipt")
				}
			}
			t.Log("real TLS MinIO: encrypted daily index, immutable replay and lost-host CLI discovery verified")
		}
		if n == 0 && os.Getenv("TENANCY_OFFSITE_CLI") != "" {
			cli := os.Getenv("TENANCY_OFFSITE_CLI")
			if !filepath.IsAbs(cli) {
				t.Fatal("absolute test CLI required")
			}
			operator := t.TempDir()
			keyFile := filepath.Join(operator, "key.bin")
			write(keyFile, key)
			journal := filepath.Join(operator, "upload-journal")
			cfg := map[string]any{"destination": config, "expected": binding, "archiveDirectory": source, "journalDirectory": journal, "actor": "fixture-operator", "reason": "isolated command test"}
			cfgFile := filepath.Join(operator, "upload.json")
			raw, _ := json.Marshal(cfg)
			write(cfgFile, raw)
			runCLI := func(mode, path string) {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), cli, "-mode", mode, "-config", path, "-key-file", keyFile, "-confirmed")
				cmd.Stderr = io.Discard
				out, e := cmd.Output()
				if e != nil || !bytes.Contains(out, []byte("verified")) {
					t.Fatal("offsite operator command failed", mode)
				}
			}
			runCLI("upload", cfgFile)
			runCLI("upload", cfgFile)
			destination := filepath.Join(operator, "download")
			cfg["archiveDirectory"] = destination
			cfg["journalDirectory"] = filepath.Join(operator, "download-journal")
			cfg["deliveryFile"] = filepath.Join(journal, "delivery.json")
			cfgFile = filepath.Join(operator, "download.json")
			raw, _ = json.Marshal(cfg)
			write(cfgFile, raw)
			runCLI("download", cfgFile)
			runCLI("download", cfgFile)
			verified, e := Open(destination, binding, key)
			if e != nil {
				t.Fatal("CLI downloaded invalid archive")
			}
			verified.Close()
			t.Log("operator CLI upload/download, immutable intent/completion journals and repeated commands verified")
		}
		own := o.objects.(*s3Objects)
		objectKey := o.root(d) + "/manifest.sealed"
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, config.Endpoint+"/"+config.Bucket+"/"+objectKey, nil)
		anonymous, e := own.transport.RoundTrip(request)
		if e != nil {
			t.Fatal("anonymous TLS check failed")
		}
		io.Copy(io.Discard, io.LimitReader(anonymous.Body, 4096))
		anonymous.Body.Close()
		if anonymous.StatusCode != http.StatusForbidden {
			t.Fatal("backup object exposed without credentials")
		}
		if e = own.putNew(t.Context(), objectKey, []byte("different bytes")); e != errObjectExists {
			t.Fatal("server ignored conditional write")
		}
		// Bypass the application's prefix checks to test actual IAM credentials.
		for other, otherConfig := range configs {
			if other == n {
				continue
			}
			forged := otherConfig
			forged.AccessKey = config.AccessKey
			forged.SecretKey = config.SecretKey
			foreign, e := NewOffsite(forged)
			if e != nil {
				t.Fatal(e)
			}
			if e = foreign.objects.putNew(t.Context(), forged.objectRoot()+"/forbidden", []byte("probe")); e == nil {
				t.Fatal("cross-owner write allowed")
			}
			reader, e := foreign.objects.(*s3Objects).client.GetObject(t.Context(), forged.Bucket, forged.objectRoot()+"/isolation-sentinel", minio.GetObjectOptions{})
			if e != nil {
				t.Fatal("unexpected lazy object initialization")
			}
			_, e = reader.Stat()
			reader.Close()
			if minio.ToErrorResponse(e).Code != "AccessDenied" {
				t.Fatal("known foreign object was not explicitly denied")
			}
			foreign.Close()
		}
		if e = own.client.RemoveObject(t.Context(), config.Bucket, objectKey, minio.RemoveObjectOptions{}); e == nil {
			t.Fatal("backup credentials could delete archives")
		}
		if n == 0 {
			chunk := chunkKey(o.root(d), "database", 0)
			obj, e := adminStore.client.GetObject(t.Context(), config.Bucket, chunk, minio.GetObjectOptions{})
			if e != nil {
				t.Fatal(e)
			}
			data, e := io.ReadAll(obj)
			obj.Close()
			if e != nil || len(data) < 1 {
				t.Fatal("remote fault fixture")
			}
			data[0] ^= 1
			if _, e = adminStore.client.PutObject(t.Context(), config.Bucket, chunk, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{DisableMultipart: true}); e != nil {
				t.Fatal("remote admin tamper fixture")
			}
			dest := filepath.Join(t.TempDir(), "tampered")
			if e = o.Retrieve(t.Context(), binding, d, dest, key); e == nil {
				t.Fatal("real remote tamper accepted")
			}
			if _, e = os.Stat(filepath.Join(dest, "manifest.sealed")); !os.IsNotExist(e) {
				t.Fatal("tamper published local marker")
			}
			if _, e = o.Deliver(t.Context(), binding, source, key); e == nil {
				t.Fatal("remote tamper silently overwritten")
			}
		}
	}
	wrongTLS := configs[0]
	wrongTLS.CAFile = ""
	bad, e := NewOffsite(wrongTLS)
	if e != nil {
		t.Fatal(e)
	}
	defer bad.Close()
	if _, e = bad.Deliver(t.Context(), b, path, key); e == nil {
		t.Fatal("untrusted certificate accepted")
	}
	t.Log("real TLS MinIO: enterprise alpha/beta and platform use separate scoped credentials; upload/download, immutable conditional PUT and cross-owner/delete denial verified")
}
