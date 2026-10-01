package tenancy

import (
	"context"
	"errors"
	"github.com/linli/im/server/internal/config"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func StartControl(o Options, h http.Handler) (*http.Server, error) {
	c, e := TLS(o)
	if e != nil {
		return nil, e
	}
	s := &http.Server{Addr: o.ControlAddr, Handler: h, TLSConfig: c, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		if e := s.ListenAndServeTLS("", ""); e != nil && !errors.Is(e, http.ErrServerClosed) {
			slog.Error("private tenancy listener failed", "error", e)
			os.Exit(1)
		}
	}()
	return s, nil
}
func RunPlatform(c config.Config, o Options) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	p, e := NewPlatform(ctx, c, o)
	cancel()
	if e != nil {
		return e
	}
	defer p.Close()
	control, e := StartControl(o, p.ControlHandler())
	if e != nil {
		return e
	}
	h := p.Handler()
	s := &http.Server{Addr: c.Addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/platform" {
			http.Redirect(w, r, "/platform/", 302)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/platform/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/platform")
		}
		h.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		if e := s.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			slog.Error("platform listener failed", "error", e)
			os.Exit(1)
		}
	}()
	slog.Info("lightweight platform started", "addr", c.Addr)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = control.Shutdown(shutdown)
	return s.Shutdown(shutdown)
}
