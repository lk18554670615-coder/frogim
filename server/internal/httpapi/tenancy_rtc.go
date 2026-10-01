package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/linli/im/server/internal/tenancy"
)

type enterpriseCallScope struct {
	User, Call string
	Epoch      int64
}

func verifyEnterpriseCallToken(raw, key, secret, tenant string) (enterpriseCallScope, error) {
	token, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(key), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return enterpriseCallScope{}, errors.New("invalid call token")
	}
	c, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return enterpriseCallScope{}, errors.New("invalid call claims")
	}
	uid, _ := c["sub"].(string)
	metadata, _ := c["metadata"].(string)
	var scope map[string]string
	if json.Unmarshal([]byte(metadata), &scope) != nil {
		return enterpriseCallScope{}, errors.New("call scope missing")
	}
	epoch, err := strconv.ParseInt(scope["enterpriseEpoch"], 10, 64)
	video, _ := c["video"].(map[string]any)
	if uid == "" || err != nil || epoch < 1 || scope["tenantId"] != tenant || scope["callId"] == "" || video["roomJoin"] != true || video["room"] != "call_"+scope["callId"] {
		return enterpriseCallScope{}, errors.New("invalid enterprise call scope")
	}
	return enterpriseCallScope{uid, scope["callId"], epoch}, nil
}

func (x *API) callScopeActive(ctx context.Context, scope enterpriseCallScope) bool {
	active, err := x.enterprise.Active(ctx, scope.User, scope.Epoch)
	if err != nil || !active {
		return false
	}
	call, err := x.app.GetCall(scope.User, scope.Call)
	return err == nil && call.Status == "accepted" && callUserCanJoin(call, scope.User)
}

// This route belongs to the existing enterprise API, never to the platform.
// LiveKit's native signaling port must remain private in enterprise mode.
func (x *API) enterpriseCallSignal(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/livekit")
	if path != "/rtc" && path != "/rtc/validate" {
		writeError(w, 404, "NOT_FOUND", "call signaling route unavailable")
		return
	}
	raw := r.URL.Query().Get("access_token")
	if raw == "" {
		raw = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	scope, err := verifyEnterpriseCallToken(raw, x.cfg.LiveKitAPIKey, x.cfg.LiveKitAPISecret, tenancy.LoadOptions().TenantID)
	if err != nil || !x.callScopeActive(r.Context(), scope) {
		writeError(w, 403, "CALL_SESSION_REVOKED", "call session is no longer active")
		return
	}
	target, err := url.Parse(x.cfg.LiveKitAPIURL)
	if err != nil {
		writeError(w, 503, "LIVEKIT_UNAVAILABLE", "call service unavailable")
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		req.URL.Path = path
		req.URL.RawPath = ""
		director(req)
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("X-Forwarded-Host")
		req.Header.Del("X-Forwarded-Proto")
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.StatusCode != http.StatusSwitchingProtocols {
			return nil
		}
		body, ok := response.Body.(io.ReadWriteCloser)
		if !ok {
			return errors.New("call upgrade unavailable")
		}
		if !x.callScopeActive(r.Context(), scope) {
			body.Close()
			return errors.New("call session revoked during connection")
		}
		guard := &callSignalBody{ReadWriteCloser: body, done: make(chan struct{})}
		response.Body = guard
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-guard.done:
					return
				case <-ticker.C:
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					active := x.callScopeActive(ctx, scope)
					cancel()
					if !active {
						guard.Close()
						return
					}
				}
			}
		}()
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeError(w, 503, "LIVEKIT_UNAVAILABLE", "call signaling unavailable")
	}
	proxy.ServeHTTP(w, r)
}

type callSignalBody struct {
	io.ReadWriteCloser
	done chan struct{}
	once sync.Once
}

func (b *callSignalBody) Close() error {
	var err error
	b.once.Do(func() { close(b.done); err = b.ReadWriteCloser.Close() })
	return err
}
