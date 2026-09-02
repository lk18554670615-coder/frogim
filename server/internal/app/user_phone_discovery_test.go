package app

import (
	"errors"
	"testing"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
)

func TestNewUsersDefaultToPhoneDiscoveryAndPreserveOptOut(t *testing.T) {
	const phone, password = "13800138000", "StrongPass123!"
	for _, method := range []string{"otp", "password", "admin", "admin_batch"} {
		t.Run(method, func(t *testing.T) {
			a, err := New(t.Context(), teststore.Memory{})
			if err != nil {
				t.Fatal(err)
			}
			var user *model.User
			switch method {
			case "otp":
				user, err = a.Login(phone, "New user")
			case "password":
				user, err = a.RegisterWithPassword(phone, "New user", password)
			case "admin":
				user, err = a.CreateAdminUser(t.Context(), "admin", phone, "New user", password, "unspecified", "test default")
			case "admin_batch":
				_, results, batchErr := a.CreateAdminUsersBatch(t.Context(), "admin", []AdminUserBatchInput{{
					ClientRow: 2, Phone: phone, Name: "New user", Password: password, Gender: "unspecified",
				}}, "test default")
				if batchErr != nil || len(results) != 1 || results[0].Status != "created" {
					t.Fatalf("batch=%+v err=%v", results, batchErr)
				}
				user = results[0].User
			}
			if err != nil || user == nil || !user.AllowSearchByPhone {
				t.Fatalf("new user=%+v err=%v", user, err)
			}
			// A new personal default must not enable the independent global switch.
			if a.SearchCapabilities()["allowSearchByPhone"] {
				t.Fatal("registration changed platform policy")
			}
			if _, err = a.SearchUsersByIdentifier(phone, "phone"); !errors.Is(err, ErrForbidden) {
				t.Fatalf("global policy bypassed: %v", err)
			}
			profile, err := a.User(user.ID)
			if err != nil {
				t.Fatal(err)
			}
			a.DecorateOwnProfile(profile)
			if profile.AllowSearchByPhone {
				t.Fatal("effective profile ignored global policy")
			}
			if err = a.UpdateSettings("admin", map[string]any{"allowSearchByPhone": true}); err != nil {
				t.Fatal(err)
			}
			matches, err := a.SearchUsersByIdentifier(phone, "phone")
			if err != nil || len(matches) != 1 || matches[0].ID != user.ID || matches[0].Phone != "" {
				t.Fatalf("new user not discoverable or phone leaked: %+v %v", matches, err)
			}
			disabled := false
			if _, err = a.UpdateUserProfile(user.ID, store.UserProfileUpdate{AllowSearchByPhone: &disabled}); err != nil {
				t.Fatal(err)
			}
			user, err = a.Login(phone, "Must not reset preferences")
			if err != nil || user.AllowSearchByPhone {
				t.Fatalf("OTP login reset opt-out: %+v %v", user, err)
			}
			if method != "otp" {
				user, err = a.PasswordLogin(phone, password)
				if err != nil || user.AllowSearchByPhone {
					t.Fatalf("password login reset opt-out: %+v %v", user, err)
				}
			}
			matches, err = a.SearchUsersByIdentifier(phone, "phone")
			if err != nil || len(matches) != 0 {
				t.Fatalf("opted-out user discoverable: %+v %v", matches, err)
			}
		})
	}
}
