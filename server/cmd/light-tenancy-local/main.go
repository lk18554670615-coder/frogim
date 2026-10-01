// Local scaffold only: creates isolated development configuration, never deploys remotely.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/crypto/bcrypt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type M = map[string]any

func main() {
	root := flag.String("root", "..", "workspace root")
	refreshConfig := flag.Bool("refresh-config", false, "refresh generated local config, preserving certificates and data volumes")
	initMedia := flag.Bool("init-media", false, "initialize local test media buckets")
	flag.Parse()
	if *initMedia {
		for _, port := range []int{18770, 18771} {
			c, e := minio.New(fmt.Sprintf("127.0.0.1:%d", port), &minio.Options{Creds: credentials.NewStaticV4("localminio", "local-minio-only-password", "")})
			check(e)
			exists, e := c.BucketExists(context.Background(), "nexachat-media")
			check(e)
			if !exists {
				check(c.MakeBucket(context.Background(), "nexachat-media", minio.MakeBucketOptions{}))
			}
		}
		fmt.Println("local media buckets ready")
		return
	}
	abs, e := filepath.Abs(*root)
	check(e)
	dir := filepath.Join(abs, "build", "light-tenancy")
	check(os.MkdirAll(dir, 0700))
	certs(dir)
	hash, e := bcrypt.GenerateFromPassword([]byte("LocalAdmin123!"), 12)
	check(e)
	cfg := filepath.Join(dir, "compose.json")
	if _, e = os.Stat(cfg); e == nil && !*refreshConfig {
		fmt.Println("local configuration already exists; retained")
		return
	}
	write(dir, "init.sql", []byte("CREATE DATABASE enterprise_a;"))
	services := M{}
	network := func(names ...string) M {
		n := M{}
		for _, name := range names {
			n[name] = M{}
		}
		return n
	}
	port := func(host, container int) string { return fmt.Sprintf("127.0.0.1:%d:%d", host, container) }
	services["postgres-a"] = M{"image": "postgres:17-alpine", "environment": M{"POSTGRES_USER": "local", "POSTGRES_PASSWORD": "local-data-only", "POSTGRES_DB": "platform"}, "volumes": []string{"pg-a:/var/lib/postgresql/data", filepath.ToSlash(filepath.Join(dir, "init.sql")) + ":/docker-entrypoint-initdb.d/init.sql:ro"}, "networks": network("data-a"), "healthcheck": M{"test": []string{"CMD-SHELL", "pg_isready -U local -d platform"}, "interval": "3s", "timeout": "3s", "retries": 20}}
	services["postgres-b"] = M{"image": "postgres:17-alpine", "environment": M{"POSTGRES_USER": "local", "POSTGRES_PASSWORD": "local-data-only", "POSTGRES_DB": "enterprise_b"}, "volumes": []string{"pg-b:/var/lib/postgresql/data"}, "networks": network("data-b"), "healthcheck": M{"test": []string{"CMD-SHELL", "pg_isready -U local"}, "interval": "3s", "timeout": "3s", "retries": 20}}
	for _, id := range []string{"a", "b"} {
		services["redis-"+id] = M{"image": "redis:8-alpine", "command": []string{"redis-server", "--requirepass", "local-cache-only", "--appendonly", "yes"}, "volumes": []string{"redis-" + id + ":/data"}, "networks": network("data-" + id)}
	}
	common := func(id, mode string) M {
		return M{"IM_MODE": mode, "IM_ADDR": ":8080", "IM_ENV": "development", "IM_DEV_MODE": "true", "IM_DEV_ALLOW_CONTAINER_BIND": "true", "IM_IP_TEST_ONLY": "true", "IM_DEV_OTP_CODE": "123456", "IM_JWT_SECRET": "local-development-" + id + "-signing-secret-not-production", "IM_ADMIN_USERNAME": "admin", "IM_ADMIN_PASSWORD_HASH": strings.ReplaceAll(string(hash), "$", "$$"), "IM_CONTROL_ADDR": ":8443", "IM_CONTROL_CERT": "/certs/cert.pem", "IM_CONTROL_KEY": "/certs/key.pem", "IM_CONTROL_CA": "/certs/ca.pem", "IM_ALLOWED_ORIGINS": "http://127.0.0.1:18700,http://127.0.0.1:18780,http://127.0.0.1:18781", "IM_PUSH_PROVIDER": "log", "IM_LOG_LEVEL": "warn", "IM_HTTP_LOG_SUCCESS_SAMPLE_RATE": "0"}
	}
	env := common("platform", "platform")
	env["IM_DATABASE_URL"] = "postgres://local:local-data-only@postgres-a:5432/platform?sslmode=disable"
	env["IM_REDIS_URL"] = "redis://:local-cache-only@redis-a:6379/0"
	env["IM_PLATFORM_STATIC_DIR"] = "/platform-ui"
	env["IM_PLATFORM_VIEWER_USERNAME"] = "viewer"
	env["IM_PLATFORM_VIEWER_PASSWORD_HASH"] = env["IM_ADMIN_PASSWORD_HASH"]
	services["platform"] = M{"image": "frogim/light-tenancy:local", "environment": env, "ports": []string{port(18700, 8080)}, "volumes": []string{filepath.ToSlash(filepath.Join(dir, "certs", "platform")) + ":/certs:ro", filepath.ToSlash(filepath.Join(dir, "platform-ui")) + ":/platform-ui:ro"}, "networks": network("control", "data-a", "edge"), "healthcheck": M{"test": []string{"CMD", "wget", "-q", "--spider", "http://127.0.0.1:8080/ready"}, "interval": "5s", "timeout": "3s", "retries": 12}, "depends_on": M{"postgres-a": M{"condition": "service_healthy"}}}
	wkdata, e := os.ReadFile(filepath.Join(abs, "infra", "wukongim", "wk.yaml"))
	check(e)
	for i, id := range []string{"a", "b"} {
		name := "enterprise-" + id
		env := common(name, "enterprise")
		env["IM_TENANT_ID"] = name
		env["IM_ENTERPRISE_API_URL"] = fmt.Sprintf("http://127.0.0.1:%d", 18701+i)
		env["IM_ENTERPRISE_MEDIA_URL"] = env["IM_ENTERPRISE_API_URL"]
		env["IM_PLATFORM_URL"] = "https://platform:8443"
		env["IM_DATABASE_URL"] = "postgres://local:local-data-only@postgres-" + id + ":5432/enterprise_" + id + "?sslmode=disable"
		env["IM_REDIS_URL"] = "redis://:local-cache-only@redis-" + id + ":6379/1"
		env["IM_S3_ENDPOINT"] = "minio-" + id + ":9000"
		env["IM_S3_PUBLIC_ENDPOINT"] = fmt.Sprintf("127.0.0.1:%d", 18770+i)
		env["IM_S3_BUCKET"] = "nexachat-media"
		env["IM_S3_ACCESS_KEY"] = "localminio"
		env["IM_S3_SECRET_KEY"] = "local-minio-only-password"
		env["IM_WUKONG_ENABLED"] = "true"
		env["IM_WUKONG_API_URL"] = "http://im-" + id + ":5001"
		env["IM_WUKONG_MANAGER_URL"] = "http://im-" + id + ":5300"
		env["IM_WUKONG_MANAGER_TOKEN"] = "local-wukong-manager-token-" + id
		env["IM_WUKONG_TOKEN_SECRET"] = "local-user-token-secret-for-enterprise-" + id
		env["IM_WUKONG_POLICY_SECRET"] = "local-policy-secret-for-enterprise-" + id
		env["IM_WUKONG_GRPC_ADDR"] = ":6970"
		env["IM_WUKONG_TCP_URL"] = fmt.Sprintf("tcp://127.0.0.1:%d", 18740+i)
		env["IM_WUKONG_WS_URL"] = fmt.Sprintf("ws://127.0.0.1:%d", 18750+i)
		env["IM_LIVEKIT_ENABLED"] = "true"
		env["IM_LIVEKIT_URL"] = fmt.Sprintf("ws://127.0.0.1:%d/livekit", 18701+i)
		env["IM_LIVEKIT_API_URL"] = "http://rtc-" + id + ":7880"
		env["IM_LIVEKIT_API_KEY"] = "devkey"
		env["IM_LIVEKIT_API_SECRET"] = "local-call-secret-for-enterprise-" + id
		services[name] = M{"image": "frogim/light-tenancy:local", "environment": env, "ports": []string{port(18701+i, 8080)}, "volumes": []string{filepath.ToSlash(filepath.Join(dir, "certs", name)) + ":/certs:ro"}, "networks": network("control", "data-"+id, "app-"+id), "healthcheck": M{"test": []string{"CMD", "wget", "-q", "--spider", "http://127.0.0.1:8080/ready"}, "interval": "5s", "timeout": "3s", "retries": 12}, "depends_on": M{"postgres-" + id: M{"condition": "service_healthy"}, "platform": M{"condition": "service_healthy"}}}
		services["minio-"+id] = M{"image": "minio/minio:RELEASE.2025-07-23T15-54-02Z", "command": []string{"server", "/data", "--console-address", ":9001"}, "environment": M{"MINIO_ROOT_USER": "localminio", "MINIO_ROOT_PASSWORD": "local-minio-only-password"}, "ports": []string{port(18770+i, 9000)}, "volumes": []string{"media-" + id + ":/data"}, "networks": network("app-" + id)}
		wk := strings.ReplaceAll(string(wkdata), "server:6970", name+":6970")
		wk = strings.ReplaceAll(wk, "http://server:8080", "http://"+name+":8080")
		write(dir, "wk-"+id+".yaml", []byte(wk))
		services["im-"+id] = M{"image": "linli/wukongim:v2.2.5-20260422-linli.3-a888f895", "environment": M{"WK_MANAGERTOKEN": "local-wukong-manager-token-" + id, "WK_TOKENAUTHON": "true", "WK_EXTERNAL_TCPADDR": env["IM_WUKONG_TCP_URL"], "WK_EXTERNAL_WSADDR": env["IM_WUKONG_WS_URL"], "WK_WEBHOOK_GRPCADDR": name + ":6970", "WK_DATASOURCE_ADDR": "http://" + name + ":8080/internal/wukong/datasource", "IM_WUKONG_POLICY_URL": "http://" + name + ":8080/internal/wukong/policy/send", "IM_WUKONG_POLICY_SECRET": env["IM_WUKONG_POLICY_SECRET"]}, "ports": []string{port(18740+i, 5100), port(18750+i, 5200)}, "volumes": []string{filepath.ToSlash(filepath.Join(dir, "wk-"+id+".yaml")) + ":/root/wukongim/wk.yaml:ro", "im-" + id + ":/root/wukongim/data", filepath.ToSlash(filepath.Join(dir, "plugins", "wk.plugin.im-policy-linux-amd64.wkp")) + ":/root/wukongim/data/plugins/wk.plugin.im-policy-linux-amd64.wkp:ro"}, "networks": network("app-" + id)}
		livekit := fmt.Sprintf("port: 7880\nrtc:\n  tcp_port: %d\n  udp_port: %d\n  use_external_ip: false\n  node_ip: 127.0.0.1\nkeys:\n  devkey: local-call-secret-for-enterprise-%s\n", 18860+i, 18960+i, id)
		write(dir, "rtc-"+id+".yaml", []byte(livekit))
		services["rtc-"+id] = M{"image": "linli/livekit:v1.13.5", "command": []string{"--config", "/config.yaml", "--dev"}, "volumes": []string{filepath.ToSlash(filepath.Join(dir, "rtc-"+id+".yaml")) + ":/config.yaml:ro"}, "ports": []string{port(18860+i, 18860+i), fmt.Sprintf("127.0.0.1:%d:%d/udp", 18960+i, 18960+i)}, "networks": network("app-" + id)}
		write(dir, "Caddy-"+id, []byte(":8080 {\n handle_path /api/* {\n reverse_proxy enterprise-"+id+":8080\n }\n handle /v2/* {\n reverse_proxy enterprise-"+id+":8080\n }\n handle_path /admin/* {\n root * /admin\n try_files {path} /index.html\n file_server\n }\n handle {\n root * /web\n try_files {path} /index.html\n header Cache-Control no-store\n file_server\n }\n}\n"))
		services["web-"+id] = M{"image": "caddy:2.10-alpine", "command": []string{"caddy", "run", "--config", "/config/Caddyfile", "--adapter", "caddyfile"}, "ports": []string{port(18780+i, 8080)}, "volumes": []string{filepath.ToSlash(filepath.Join(dir, "Caddy-"+id)) + ":/config/Caddyfile:ro", filepath.ToSlash(filepath.Join(abs, "apps", "mobile", "build", "web")) + ":/web:ro", filepath.ToSlash(filepath.Join(dir, "enterprise-ui")) + ":/admin:ro"}, "networks": network("app-" + id)}
	}
	volumes := M{}
	for _, v := range []string{"pg-a", "pg-b", "redis-a", "redis-b", "media-a", "media-b", "im-a", "im-b"} {
		volumes[v] = M{}
	}
	networks := M{"control": M{"internal": true}, "data-a": M{"internal": true}, "data-b": M{"internal": true}, "app-a": M{}, "app-b": M{}, "edge": M{}}
	compose := M{"name": "frogim-light", "services": services, "volumes": volumes, "networks": networks}
	b, e := json.MarshalIndent(compose, "", "  ")
	check(e)
	write(dir, "compose.json", b)
	fmt.Println("created isolated local configuration")
}
func check(e error) {
	if e != nil {
		panic(e)
	}
}
func write(root, name string, b []byte) { check(os.WriteFile(filepath.Join(root, name), b, 0600)) }
func certs(root string) {
	if _, e := os.Stat(filepath.Join(root, "certs", "platform", "cert.pem")); e == nil {
		return
	}
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(e)
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Local tenancy CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	check(e)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	for i, name := range []string{"platform", "enterprise-a", "enterprise-b"} {
		dir := filepath.Join(root, "certs", name)
		check(os.MkdirAll(dir, 0700))
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		check(e)
		c := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		d, e := x509.CreateCertificate(rand.Reader, c, ca, &k.PublicKey, caKey)
		check(e)
		key, e := x509.MarshalPKCS8PrivateKey(k)
		check(e)
		write(dir, "cert.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}))
		write(dir, "key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
		write(dir, "ca.pem", caPEM)
	}
}
