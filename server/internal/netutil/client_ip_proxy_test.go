package netutil

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in: creates and removes only its own disposable Caddy container.
func TestClientIPRealCaddy(t *testing.T) {
	if os.Getenv("IM_TEST_CADDY") != "1" {
		t.Skip("IM_TEST_CADDY=1 requires Docker and caddy:2.10-alpine")
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, NormalizeIP(ClientIP(r, true)))
	}), ReadHeaderTimeout: time.Second}
	go server.Serve(listener)
	defer server.Close()
	name := fmt.Sprintf("im-ip-proxy-test-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	target := fmt.Sprintf("host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port)
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "-d", "--name", name, "-p", "127.0.0.1::80", "caddy:2.10-alpine", "caddy", "reverse-proxy", "--from", ":80", "--to", target).CombinedOutput()
	if err != nil {
		t.Fatalf("Caddy start: %v %s", err, out)
	}
	defer exec.Command("docker", "rm", "-f", name).Run()
	out, err = exec.CommandContext(ctx, "docker", "port", name, "80/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + strings.TrimSpace(string(out))
	client := &http.Client{Timeout: time.Second}
	request := func(forwarded string) (string, error) {
		r, _ := http.NewRequest("GET", url, nil)
		r.Header.Set("X-Forwarded-For", forwarded)
		r.Header.Set("X-Real-IP", "8.8.8.8")
		res, e := client.Do(r)
		if e != nil {
			return "", e
		}
		defer res.Body.Close()
		body, e := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			return "", fmt.Errorf("HTTP %d", res.StatusCode)
		}
		return string(body), e
	}
	var actual string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		actual, err = request("")
		if err == nil {
			break
		}
	}
	if err != nil || NormalizeIP(actual) == "" {
		t.Fatalf("proxy unavailable: %q %v", actual, err)
	}
	for _, forged := range []string{"8.8.8.8", "8.8.8.8, 1.1.1.1", "2001:4860:4860::8888", "::ffff:8.8.8.8"} {
		got, e := request(forged)
		if e != nil || got != actual || got == "8.8.8.8" {
			t.Fatalf("forged %s: got %s, baseline %s, %v", forged, got, actual, e)
		}
	}
	t.Logf("Caddy normalized peer %s; forged IPv4/IPv6 forwarding headers discarded", actual)
}
