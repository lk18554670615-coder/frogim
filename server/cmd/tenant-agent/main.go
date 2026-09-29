// Independent mTLS identity. Execution is opt-in, fixed-target and catalog-only;
// the default local agent remains inspection-only without a Docker socket.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

func main() {
	if run() != nil {
		slog.Error("tenant agent failed; inspect private local configuration")
		os.Exit(1)
	}
}
func run() error {
	if err := validateAgentRuntime(os.Getenv, runtime.GOOS); err != nil {
		return err
	}
	hostFile := os.Getenv("AGENT_HOST_ID_FILE")
	if os.Getenv("AGENT_ISOLATION_MODE") == "dedicated_host" && hostFile != "/etc/machine-id" {
		return errors.New("dedicated host identity must be read from the operating system")
	}
	hostID, err := os.ReadFile(hostFile)
	if err != nil {
		return errors.New("host identity unavailable")
	}
	agent, err := deployment.New(os.Getenv("AGENT_SERVER_ID"), os.Getenv("AGENT_TENANT_ID"), os.Getenv("AGENT_TENANT_HTTP_URL"), os.Getenv("AGENT_ISOLATION_MODE"), hostID)
	if err != nil {
		return err
	}
	defer agent.Close()
	cfg, err := tenancy.TLSConfig(os.Getenv("AGENT_CA_FILE"), os.Getenv("AGENT_CERT_FILE"), os.Getenv("AGENT_KEY_FILE"))
	if err != nil {
		return err
	}
	if tenancy.ValidateLocalIdentity(cfg, deployment.AgentIdentity(os.Getenv("AGENT_SERVER_ID"))) != nil {
		return errors.New("agent local certificate identity mismatch")
	}
	// Validate identity before opening a writable execution journal.
	if path := os.Getenv("AGENT_EXECUTOR_FILE"); path != "" {
		if err = agent.ConfigureExecutor(path); err != nil {
			return err
		}
	}
	addr := os.Getenv("AGENT_ADDR")
	if addr == "" {
		addr = ":8450"
	}
	control := &http.Server{Addr: addr, TLSConfig: cfg, Handler: agent.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	// Loopback health exposes no identity or control operation.
	health := &http.Server{Addr: "127.0.0.1:8451", ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(204)
	})}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go agent.RunExecutor(ctx)
	done := make(chan error, 2)
	go func() { done <- control.ListenAndServeTLS("", "") }()
	go func() { done <- health.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err = <-done:
		stop()
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = control.Shutdown(closeCtx)
	_ = health.Shutdown(closeCtx)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
