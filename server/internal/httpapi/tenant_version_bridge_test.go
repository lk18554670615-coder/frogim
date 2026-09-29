package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/linli/im/server/internal/clientversion"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/tenancy"
)

func TestTenantLegacyUpgradeAddressUsesAuthenticatedPlatformPolicy(t *testing.T) {
	configs, _ := tenancyStackPKI(t)
	platform := tenancyStackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/tenancy/client-version" || tenancy.PeerIdentity(r) != tenancy.EnterpriseIdentity("a") {
			w.WriteHeader(403)
			return
		}
		var q map[string]string
		if json.NewDecoder(r.Body).Decode(&q) != nil || q["platform"] != "ios" || q["version"] != "1.0.0" || q["installId"] != "old-install-id" {
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(clientversion.Decision{Platform: "ios", CurrentVersion: "1.0.0", MinimumVersion: "2.0.0", LatestVersion: "2.0.0", ForceUpdate: true, UpdateAvailable: true, DownloadURL: "https://upgrade.example.test/ios"})
	}), configs["platform"], true)
	rpc, e := tenancy.NewRPC(platform.URL, configs["a"], tenancy.PlatformIdentity)
	if e != nil {
		t.Fatal(e)
	}
	x := &API{cfg: config.Config{TenantID: "a"}, platformControl: rpc}
	request := httptest.NewRequest("GET", "/v2/config/version?platform=ios&version=1.0.0&installId=old-install-id", nil)
	w := httptest.NewRecorder()
	if !x.allowTenantRoute(w, request) {
		t.Fatal("legacy upgrade blocked")
	}
	x.clientVersion(w, request)
	var body struct {
		Data clientversion.Decision `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || !body.Data.ForceUpdate || body.Data.DownloadURL != "https://upgrade.example.test/ios" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("platform policy lost", w.Code)
	}
	platform.Close()
	w = httptest.NewRecorder()
	x.clientVersion(w, request)
	if w.Code != 503 {
		t.Fatal("offline platform fell back to old policy", w.Code)
	}
}
