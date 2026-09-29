package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	livekitcontrol "github.com/linli/im/server/internal/livekit"
	"github.com/linli/im/server/internal/tenancy"
)

type tenantSignalRoundTrip func(*http.Request) (*http.Response, error)

func (f tenantSignalRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTenantSignalDurableBarrier(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		networkErr, denied, persistenceErr bool
		status                             int
		path                               string
	}{
		{name: "successful join", status: 101, path: "/rtc"},
		{name: "validate does not start join", status: 200, path: "/rtc/validate"},
		{name: "uncertain response stays pending", networkErr: true, path: "/rtc"},
		{name: "non-upgrade stays pending", status: 500, path: "/rtc"},
		{name: "revoked before upstream", denied: true, path: "/rtc"},
		{name: "completion persistence failure", persistenceErr: true, status: 101, path: "/rtc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			locked, pending, called := false, false, false
			p := livekitcontrol.TenantParticipant{Identity: tenancy.Identity{TenantID: "a", AccountID: "account", LocalUserID: "user", AssignmentVersion: 1}, AuthVersion: 1}
			transport := tenantSignalTransport{
				fence: func(_ context.Context, user string, fn func() error) error {
					if user != "user" {
						t.Fatal("wrong fence")
					}
					locked = true
					defer func() { locked = false }()
					return fn()
				},
				authorize: func(context.Context, livekitcontrol.TenantParticipant) error {
					if !locked {
						t.Fatal("unfenced authorization")
					}
					if tc.denied {
						return app.ErrForbidden
					}
					return nil
				},
				begin: func(context.Context, tenancy.Identity, int64, int64) (string, error) {
					if !locked {
						t.Fatal("unfenced begin")
					}
					pending = true
					return "attempt", nil
				},
				complete: func(_ context.Context, id string) error {
					if !locked || id != "attempt" {
						t.Fatal("unfenced completion")
					}
					if tc.persistenceErr {
						return errors.New("database unavailable")
					}
					pending = false
					return nil
				},
				base: tenantSignalRoundTrip(func(*http.Request) (*http.Response, error) {
					called = true
					if !locked {
						t.Fatal("unfenced handshake")
					}
					if tc.networkErr {
						return nil, errors.New("connection lost")
					}
					return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(""))}, nil
				}),
			}
			r := httptest.NewRequest("GET", "http://private"+tc.path, nil).WithContext(context.WithValue(t.Context(), tenantSignalKey{}, p))
			response, err := transport.RoundTrip(r)
			if locked {
				t.Fatal("lock kept for entire call")
			}
			if (err != nil) != (tc.networkErr || tc.denied || tc.persistenceErr) {
				t.Fatal("wrong result", err)
			}
			if called == tc.denied {
				t.Fatal("authorization did not guard upstream")
			}
			wantPending := !tc.denied && tc.path != "/rtc/validate" && (tc.networkErr || tc.persistenceErr || tc.status != 101)
			if pending != wantPending {
				t.Fatal("unsafe durable barrier cleanup")
			}
			if err != nil && response != nil {
				t.Fatal("uncertain upstream returned")
			}
		})
	}
}

func TestTenantSignalOnlyAllowsScopedRTC(t *testing.T) {
	control, err := livekitcontrol.NewControl(livekitcontrol.Config{URL: "wss://enterprise.example/livekit", APIURL: "http://127.0.0.1:7880", APIKey: "a", APISecret: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	identity := tenancy.Identity{AccountID: "account", TenantID: "a", LocalUserID: "user", AssignmentVersion: 1}
	session, err := control.IssueTenantParticipant("call", "conv", "audio", identity, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	foreign := identity
	foreign.TenantID = "b"
	other, err := control.IssueTenantParticipant("call", "conv", "audio", foreign, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, token, authorization, origin string
		status                                   int
	}{
		{name: "valid", path: "/livekit/rtc/validate", token: session.Token, status: 204},
		{name: "untrusted origin", path: "/livekit/rtc/validate", token: session.Token, origin: "https://evil.example", status: 403},
		{name: "foreign enterprise", path: "/livekit/rtc/validate", token: other.Token, status: 401},
		{name: "missing token", path: "/livekit/rtc/validate", status: 401},
		{name: "conflicting credentials", path: "/livekit/rtc/validate", token: session.Token, authorization: "Bearer different", status: 401},
		{name: "publish override", path: "/livekit/rtc/validate?publish=another", token: session.Token, status: 401},
		{name: "duplicate token", path: "/livekit/rtc/validate?access_token=other", token: session.Token, status: 401},
		{name: "admin path", path: "/livekit/twirp/livekit.RoomService/ListRooms", token: session.Token, status: 404},
		{name: "rtc prefix bypass", path: "/livekit/rtc/evil", token: session.Token, status: 404},
		{name: "invalid handshake", path: "/livekit/rtc", token: session.Token, status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			x := &API{cfg: config.Config{TenantID: "a", AllowedOrigins: []string{"https://app.example"}}, livekit: control, tenantMediaProxy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if _, ok := r.Context().Value(tenantSignalKey{}).(livekitcontrol.TenantParticipant); !ok {
					t.Error("missing validated identity")
				}
				w.WriteHeader(204)
			})}
			path := tc.path
			if tc.token != "" {
				join := "?"
				if strings.Contains(path, "?") {
					join = "&"
				}
				path += join + "access_token=" + tc.token
			}
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Authorization", tc.authorization)
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			x.tenantLiveKitSignal(w, r)
			if w.Code != tc.status || called != (tc.status == 204) {
				t.Fatal("signal boundary", w.Code, called)
			}
			if strings.Contains(w.Body.String(), session.Token) || strings.Contains(w.Body.String(), other.Token) {
				t.Fatal("response leaked token")
			}
		})
	}
}
