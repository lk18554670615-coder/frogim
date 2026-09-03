package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

func TestOwnProfileCapabilitiesAcrossLoginReadAndPhoneUpdate(t *testing.T) {
	for used := 0; used <= 2; used++ {
		t.Run(fmt.Sprintf("used_%d", used), func(t *testing.T) {
			a, err := app.New(context.Background(), teststore.Memory{})
			if err != nil {
				t.Fatal(err)
			}
			user, err := a.Login("13900000991", "Profile test")
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < used; i++ {
				handle := fmt.Sprintf("profile_test_%d", i)
				if _, err = a.UpdateUserProfile(user.ID, store.UserProfileUpdate{Handle: &handle}); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.Config{JWTSecret: "profile-test-secret", DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}
			ts := httptest.NewServer(New(cfg, a).Handler())
			defer ts.Close()
			res := publicPlatformPost(t, ts.URL+"/v2/auth/login", "android", `{"phone":"13900000991","code":"654321"}`)
			var login struct {
				User        model.User
				AccessToken string
			}
			if err = json.NewDecoder(res.Body).Decode(&login); err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatalf("login status=%d", res.StatusCode)
			}
			assertProfile := func(profile model.User) {
				t.Helper()
				if profile.HandleChangeCount != used || profile.HandleChangesRemaining != 2-used {
					t.Fatalf("used=%d: count=%d remaining=%d", used, profile.HandleChangeCount, profile.HandleChangesRemaining)
				}
			}
			assertProfile(login.User)
			for _, request := range []struct{ method, path, body string }{
				{http.MethodGet, "/v2/users/me", ""},
				{http.MethodPatch, "/v2/users/me", `{"name":"New nickname","signature":"New signature","gender":"female"}`},
				{http.MethodPatch, "/v2/users/me/phone", `{"phone":"13900000992","code":"654321"}`},
			} {
				res = authenticatedRequest(t, request.method, ts.URL+request.path, login.AccessToken, request.body)
				var profile model.User
				if err = json.NewDecoder(res.Body).Decode(&profile); err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != 200 {
					t.Fatalf("%s %s: status=%d", request.method, request.path, res.StatusCode)
				}
				assertProfile(profile)
			}
			// Serializing a login response must not modify the shared store object.
			if user.HandleChangesRemaining != 0 {
				t.Fatal("response decoration mutated stored user")
			}
			if used == 2 {
				res = authenticatedRequest(t, http.MethodPatch, ts.URL+"/v2/users/me", login.AccessToken, `{"handle":"another_handle"}`)
				res.Body.Close()
				if res.StatusCode != 403 {
					t.Fatalf("handle quota no longer enforced: %d", res.StatusCode)
				}
			}
		})
	}
}
