package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/linli/im/server/internal/clientversion"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
)

// Explicit local CA verification; never InsecureSkipVerify, no global trust
// changes, redirects, credentials in URLs, or response bodies in diagnostics.
func verifyLocal(root string, adminCreate, passwordReset, verifyAgent bool) error {
	ca, err := os.ReadFile(filepath.Join(root, "browser-ca.pem"))
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("invalid local browser CA")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	data, err := os.ReadFile(filepath.Join(root, "credentials.json"))
	if err != nil {
		return err
	}
	var creds localCredentials
	if json.Unmarshal(data, &creds) != nil {
		return errors.New("invalid local credentials")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	call := func(method, base, path, token string, input, output any, want int) error {
		body, err := json.Marshal(input)
		if err != nil {
			return err
		}
		r, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Client-Platform", "web")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(r)
		if err != nil {
			return fmt.Errorf("local %s request failed (transport)", path)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			return fmt.Errorf("local %s returned %d, expected %d", path, response.StatusCode, want)
		}
		if output != nil {
			return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(output)
		}
		return nil
	}
	const p = "https://127.0.0.1:18443"
	const e = "https://127.0.0.1:18444"
	var pushConfiguration struct {
		Enabled        bool
		Providers      []string
		WebPushEnabled bool
	}
	if err = call("GET", p, "/v2/config/push", "", nil, &pushConfiguration, 200); err != nil {
		return err
	}
	if pushConfiguration.Enabled || pushConfiguration.WebPushEnabled || len(pushConfiguration.Providers) != 0 {
		return errors.New("default local preview must not enable external push providers")
	}
	if err = call("POST", p, "/internal/tenancy/push/deliver", "", map[string]any{}, nil, 404); err != nil {
		return err
	}
	if err = call("POST", e, "/v2/users/me/devices", "", map[string]any{}, nil, 409); err != nil {
		return err
	}
	fmt.Println("PASS: local platform push disabled; private delivery hidden; enterprise rejects local token registration. No device binding or external notification created.")
	var enterpriseAdmin struct{ AccessToken string }
	if err = call("POST", e, "/v2/admin/auth/login", "", map[string]string{"username": "enterprise-admin", "password": creds.EnterpriseAdmin}, &enterpriseAdmin, 200); err != nil {
		return err
	}
	var adminJobs struct {
		Managed bool
		Items   []platform.TenantAccountJob
	}
	if err = call("GET", e, "/v2/admin/users/provisioning-jobs", enterpriseAdmin.AccessToken, nil, &adminJobs, 200); err != nil {
		return err
	}
	if !adminJobs.Managed {
		return errors.New("enterprise account directory bridge is not active")
	}
	if adminCreate {
		// Stable local-only fixture. Do not overwrite an existing phone; the
		// normal platform uniqueness and request-content checks must succeed.
		input := map[string]any{"requestId": "local-admin-create-v1", "phone": "19900000002", "name": "本机企业开户验证", "gender": "unspecified", "password": creds.UserPassword, "reason": "本机多租户开户链路验证", "confirmed": true}
		var created struct{ Item platform.TenantAccountJob }
		if err = call("POST", e, "/v2/admin/users", enterpriseAdmin.AccessToken, input, &created, 202); err != nil {
			return err
		}
		for attempt := 0; ; attempt++ {
			if err = call("GET", e, "/v2/admin/users/provisioning-jobs?jobId="+created.Item.JobID, enterpriseAdmin.AccessToken, nil, &adminJobs, 200); err != nil {
				return err
			}
			if len(adminJobs.Items) != 1 {
				return errors.New("local admin task not visible in same enterprise")
			}
			if adminJobs.Items[0].Status == "completed" {
				break
			}
			if attempt >= 20 || adminJobs.Items[0].Status == "blocked" {
				return errors.New("local admin task did not complete")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
		var replay struct{ Item platform.TenantAccountJob }
		if err = call("POST", e, "/v2/admin/users", enterpriseAdmin.AccessToken, input, &replay, 202); err != nil {
			return err
		}
		if replay.Item.JobID != created.Item.JobID || replay.Item.Status != "completed" {
			return errors.New("local admin request replay was not idempotent")
		}
		var newlyCreated platform.LoginResult
		if err = call("POST", p, "/v2/auth/password-login", "", map[string]string{"phone": "19900000002", "password": creds.UserPassword}, &newlyCreated, 200); err != nil {
			return err
		}
		if newlyCreated.TenantContext.TenantID != "default" {
			return errors.New("admin-created account assigned to unexpected tenant")
		}
		if passwordReset {
			localID := adminJobs.Items[0].LocalUserID
			reset := map[string]any{"requestId": "local-admin-password-v1", "newPassword": creds.UserPassword, "reason": "本机统一密码任务验证", "confirmed": true}
			var task struct{ Item platform.CredentialJob }
			if err = call("POST", e, "/v2/admin/users/"+localID+"/tenant-password-reset", enterpriseAdmin.AccessToken, reset, &task, 200); err != nil {
				return err
			}
			for attempt := 0; ; attempt++ {
				if err = call("GET", e, "/v2/admin/users/"+localID+"/credential-jobs/"+task.Item.ID, enterpriseAdmin.AccessToken, nil, &task, 200); err != nil {
					return err
				}
				if task.Item.Status == "completed" {
					break
				}
				if attempt >= 20 {
					return errors.New("local password task still pending")
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
			var repeated struct{ Item platform.CredentialJob }
			if err = call("POST", e, "/v2/admin/users/"+localID+"/tenant-password-reset", enterpriseAdmin.AccessToken, reset, &repeated, 200); err != nil {
				return err
			}
			if repeated.Item.ID != task.Item.ID || repeated.Item.Status != "completed" {
				return errors.New("local password task replay changed")
			}
			var jobs struct {
				Managed bool
				Items   []platform.CredentialJob
			}
			if err = call("GET", e, "/v2/admin/users/"+localID+"/credential-jobs", enterpriseAdmin.AccessToken, nil, &jobs, 200); err != nil {
				return err
			}
			if !jobs.Managed || len(jobs.Items) < 1 {
				return errors.New("local password jobs not visible")
			}
			// New login succeeds with the same random local fixture password; the
			// distinct old/new password and stale-token cases live in isolated tests.
			var after platform.LoginResult
			if err = call("POST", p, "/v2/auth/password-login", "", map[string]string{"phone": "19900000002", "password": creds.UserPassword}, &after, 200); err != nil {
				return err
			}
			if err = call("POST", p, "/v2/auth/logout", "", map[string]string{"refreshToken": after.RefreshToken}, nil, 200); err != nil {
				return err
			}
			fmt.Println("PASS: local admin password task completed; replay and scoped task list; platform login. Fixture password unchanged; no production operation.")
		}
		if err = call("POST", p, "/v2/auth/logout", "", map[string]string{"refreshToken": newlyCreated.RefreshToken}, nil, 200); err != nil {
			return err
		}
		fmt.Println("PASS: local enterprise admin creation; durable completion; identical request replay; platform password login into default tenant. One local fixture account retained, no production change.")
	}
	var login struct{ AccessToken string }
	if err = call("POST", p, "/platform/admin/auth/login", "", map[string]string{"username": "operator", "password": creds.PlatformAdmin}, &login, 200); err != nil {
		return err
	}
	defer call("POST", p, "/platform/admin/auth/logout", login.AccessToken, struct{}{}, nil, 200)
	var deployments struct {
		Items []platform.DeploymentJob
		Total int
	}
	if err = call("GET", p, "/platform/admin/deployments", login.AccessToken, nil, &deployments, 200); err != nil {
		return err
	}
	var deploymentReleases struct{ Items []json.RawMessage }
	if err = call("GET", p, "/platform/admin/deployment-releases", login.AccessToken, nil, &deploymentReleases, 200); err != nil {
		return err
	}
	if deployments.Total != 0 || len(deployments.Items) != 0 || len(deploymentReleases.Items) != 0 {
		return errors.New("default local preview must not implicitly enable or execute managed deployment")
	}
	for _, path := range []string{"/platform/admin/deployments", "/platform/admin/deployment-releases"} {
		if err = call("GET", p, path, "", nil, nil, 401); err != nil {
			return err
		}
	}
	if err = call("POST", p, "/internal/agent/deployment/submit", "", map[string]any{}, nil, 404); err != nil {
		return err
	}
	fmt.Println("PASS: deployment task and catalog authentication; empty local catalog and no deployment jobs; private agent execution route not exposed. No deployment dispatched.")
	var backups struct {
		Items []platform.BackupJob
		Total int
	}
	if err = call("GET", p, "/platform/admin/backups", login.AccessToken, nil, &backups, 200); err != nil {
		return err
	}
	if backups.Total != 0 || len(backups.Items) != 0 {
		return errors.New("default local preview must not implicitly start backups")
	}
	if err = call("GET", p, "/platform/admin/backups", "", nil, nil, 401); err != nil {
		return err
	}
	for _, path := range []string{"/platform/admin/backup-schedules", "/platform/admin/maintenance"} {
		if err = call("GET", p, path, login.AccessToken, nil, &backups, 200); err != nil {
			return err
		}
		if backups.Total != 0 || len(backups.Items) != 0 {
			return errors.New("default local preview must not enable daily maintenance")
		}
		if err = call("GET", p, path, "", nil, nil, 401); err != nil {
			return err
		}
	}
	fmt.Println("PASS: daily schedules and maintenance authentication; no default schedule or automatic task enabled.")
	if err = call("POST", p, "/internal/agent/backup/submit", "", map[string]any{}, nil, 404); err != nil {
		return err
	}
	fmt.Println("PASS: backup task authentication, no default backup jobs and no public agent backup route. No backup or maintenance dispatched.")
	var accessJobs struct {
		Items []platform.AccessJob
		Total int
	}
	if err = call("GET", p, "/platform/admin/access-jobs?pageSize=1", login.AccessToken, nil, &accessJobs, 200); err != nil {
		return err
	}
	if err = call("GET", p, "/platform/admin/access-jobs", "", nil, nil, 401); err != nil {
		return err
	}
	var realmJobs struct {
		Items []platform.RealmJob
		Total int
	}
	if err = call("GET", p, "/platform/admin/realm-jobs?pageSize=1", login.AccessToken, nil, &realmJobs, 200); err != nil {
		return err
	}
	if err = call("GET", p, "/platform/admin/realm-jobs", "", nil, nil, 401); err != nil {
		return err
	}
	var administrators struct {
		Items []platform.Administrator
		Total int
	}
	if err = call("GET", p, "/platform/admin/administrators?q=operator&state=enabled", login.AccessToken, nil, &administrators, 200); err != nil {
		return err
	}
	if administrators.Total != 1 || len(administrators.Items) != 1 || administrators.Items[0].Username != "operator" || administrators.Items[0].Role != "operator" {
		return errors.New("local platform administrator unavailable")
	}
	if err = call("GET", p, "/platform/admin/administrators", "", nil, nil, 401); err != nil {
		return err
	}
	fmt.Println("PASS: platform administrator list and authentication boundary; local credentials and roles unchanged.")
	var releases struct {
		Items []platform.ClientRelease
		Total int
	}
	if err = call("GET", p, "/platform/admin/client-versions", login.AccessToken, nil, &releases, 200); err != nil {
		return err
	}
	if releases.Total != 4 || len(releases.Items) != 4 {
		return errors.New("platform client policies incomplete")
	}
	if err = call("GET", p, "/platform/admin/client-versions", "", nil, nil, 401); err != nil {
		return err
	}
	for _, target := range []string{"android", "ios", "web", "macos"} {
		var decision struct{ Data clientversion.Decision }
		if err = call("GET", p, "/v2/config/version?platform="+target+"&version=1.0.12&installId=local-readonly-check", "", nil, &decision, 200); err != nil {
			return err
		}
		if decision.Data.Platform != target || decision.Data.CurrentVersion != "1.0.12" {
			return errors.New("platform client version contract mismatch")
		}
	}
	// The managed enterprise cannot read or publish the global rollout policy.
	// Empty input is intentional: the ownership gate must run before any write.
	for _, check := range []struct{ method, path string }{{"GET", "/v2/config/version"}, {"GET", "/v2/admin/client-versions"}, {"PUT", "/v2/admin/client-versions/android"}} {
		if err = call(check.method, e, check.path, enterpriseAdmin.AccessToken, struct{}{}, nil, 409); err != nil {
			return err
		}
	}
	fmt.Println("PASS: four platform client version contracts; management auth required; enterprise rollout ownership denied. No update policy published.")
	var tenants struct {
		Items []struct {
			ID, Status, HTTPBaseURL string
			ConfigVersion           int64
		}
		Total int
	}
	if err = call("GET", p, "/platform/admin/tenants", login.AccessToken, nil, &tenants, 200); err != nil {
		return err
	}
	if tenants.Total != 1 || len(tenants.Items) != 1 || tenants.Items[0].ID != "default" || tenants.Items[0].Status != "active" || tenants.Items[0].HTTPBaseURL != e {
		return errors.New("local tenant topology does not match default-only configuration")
	}
	if verifyAgent {
		var configured struct{ ServerIDs []string }
		if err = call("GET", p, "/platform/admin/servers/configured", login.AccessToken, nil, &configured, 200); err != nil {
			return err
		}
		if len(configured.ServerIDs) != 1 || configured.ServerIDs[0] != "default-local" {
			return errors.New("unexpected local inspection agent configuration")
		}
		var registered struct {
			Items []platform.Server
			Total int
		}
		if err = call("GET", p, "/platform/admin/servers", login.AccessToken, nil, &registered, 200); err != nil {
			return err
		}
		in := platform.ServerOperation{RequestID: "local-default-server-v1", Action: "register", ServerID: "default-local", TenantID: "default", DisplayName: "本机默认企业（非物理隔离）", ExpectedConfigVersion: tenants.Items[0].ConfigVersion, Reason: "explicit local default-only agent verification", Confirmed: true}
		if registered.Total > 1 {
			return errors.New("unexpected additional local servers")
		}
		if registered.Total == 1 {
			if len(registered.Items) != 1 || registered.Items[0].ID != in.ServerID || registered.Items[0].TenantID != "default" || registered.Items[0].IsolationMode != "local_preview" {
				return errors.New("local server binding does not match")
			}
			in.Action = "inspect"
			in.DisplayName = registered.Items[0].DisplayName
			in.ExpectedRevision = registered.Items[0].Revision
			in.RequestID, err = tenancy.Secret()
			if err != nil {
				return err
			}
			in.RequestID = "local-inspect-" + in.RequestID
		}
		var result, replay platform.Server
		if err = call("POST", p, "/platform/admin/servers/operations", login.AccessToken, in, &result, 200); err != nil {
			return err
		}
		if err = call("POST", p, "/platform/admin/servers/operations", login.AccessToken, in, &replay, 200); err != nil {
			return err
		}
		if result.ID != in.ServerID || result.TenantID != "default" || result.IsolationMode != "local_preview" || result.Revision != in.ExpectedRevision+1 || result.Revision != replay.Revision || !result.VerifiedAt.Equal(replay.VerifiedAt) {
			return errors.New("local agent inspection/replay mismatch")
		}
		if err = call("GET", p, "/platform/admin/servers", "", nil, nil, 401); err != nil {
			return err
		}
		if err = call("POST", p, "/internal/agent/inspect", "", map[string]any{}, nil, 404); err != nil {
			return err
		}
		fmt.Println("PASS: default-only mTLS agent binding/inspection, immutable replay, authentication and private route boundaries. Local preview only; no deployment or command executed.")
	}
	for i := 0; i < 25; i++ {
		var accounts struct{ Items []struct{ State string } }
		if err = call("GET", p, "/platform/admin/accounts?q="+creds.UserPhone, login.AccessToken, nil, &accounts, 200); err != nil {
			return err
		}
		if len(accounts.Items) == 1 && accounts.Items[0].State == "active" {
			break
		}
		if i == 24 {
			return errors.New("local account saga did not finish")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	var policy struct{ OTPLoginEnabled, RegistrationEnabled, PasswordResetEnabled, TenantAuthentication bool }
	if err = call("GET", p, "/v2/config/auth", "", nil, &policy, 200); err != nil {
		return err
	}
	if policy.OTPLoginEnabled || policy.RegistrationEnabled || policy.PasswordResetEnabled || !policy.TenantAuthentication {
		return errors.New("local real-OTP-unconfigured policy is unsafe")
	}
	for _, path := range []string{"/v2/auth/password-reset/code", "/v2/auth/password-reset"} {
		if err = call("POST", p, path, "", map[string]any{}, nil, 503); err != nil {
			return err
		}
	}
	var session platform.LoginResult
	if err = call("POST", p, "/v2/auth/password-login", "", map[string]string{"phone": creds.UserPhone, "password": creds.UserPassword}, &session, 200); err != nil {
		return err
	}
	if session.TenantContext.TenantID != "default" || session.TenantContext.HTTPBaseURL != e {
		return errors.New("unexpected tenant routing")
	}
	var business struct {
		AccessToken string
		User        struct{ ID string }
		ImSession   struct{ UID string }
	}
	if err = call("POST", e, "/v2/auth/tenant-session", "", map[string]string{"sessionTicket": session.SessionTicket}, &business, 200); err != nil {
		return err
	}
	if business.AccessToken == "" || business.User.ID == "" || business.User.ID != business.ImSession.UID {
		return errors.New("business IM identity mismatch")
	}
	var me struct{ ID string }
	if err = call("GET", e, "/v2/users/me", business.AccessToken, nil, &me, 200); err != nil {
		return err
	}
	if me.ID != business.User.ID {
		return errors.New("profile identity mismatch")
	}
	if err = call("POST", e, "/v2/auth/tenant-session", "", map[string]string{"sessionTicket": session.SessionTicket}, nil, 401); err != nil {
		return err
	}
	if err = call("GET", p, "/v2/users/me", business.AccessToken, nil, nil, 404); err != nil {
		return err
	}
	if err = call("GET", e, "/v2/users/me", login.AccessToken, nil, nil, 401); err != nil {
		return err
	}
	if err = call("GET", p, "/platform/admin/tenants", business.AccessToken, nil, nil, 401); err != nil {
		return err
	}
	for _, base := range []string{p, e} {
		if err = call("POST", base, "/internal/tenancy/readiness", "", map[string]string{"nonce": "not-public"}, nil, 404); err != nil {
			return err
		}
	}
	// Public signaling must hit the enterprise identity guard, not the raw
	// LiveKit listener or its privileged Twirp management API.
	if err = call("GET", e, "/livekit/twirp/livekit.RoomService/ListRooms", "", nil, nil, 404); err != nil {
		return err
	}
	if err = call("GET", e, "/livekit/rtc/validate", business.AccessToken, nil, nil, 401); err != nil {
		return err
	}
	if err = call("GET", e, "/livekit/rtc", "", nil, nil, 400); err != nil {
		return err
	}
	if err = call("POST", p, "/v2/auth/logout", "", map[string]string{"refreshToken": session.RefreshToken}, nil, 200); err != nil {
		return err
	}
	if err = call("POST", p, "/v2/auth/refresh", "", map[string]string{"refreshToken": session.RefreshToken}, nil, 401); err != nil {
		return err
	}
	fmt.Println("PASS: default-only topology; verified gateway TLS; platform password login; one-use ticket; direct enterprise API/IM identity; replay rejection; realm separation; private route exclusion; guarded LiveKit and no public Twirp; logout invalidates refresh; unconfigured login OTP and password-recovery SMS disabled.")
	return nil
}
