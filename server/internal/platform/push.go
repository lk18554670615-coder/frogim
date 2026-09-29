package platform

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
	"github.com/linli/im/server/internal/webpushpolicy"
)

type PushDevice struct {
	DeviceID             string `json:"deviceId"`
	Platform             string `json:"platform"`
	Provider             string `json:"provider"`
	PushToken            string `json:"pushToken"`
	NotificationsEnabled bool   `json:"notificationsEnabled"`
	PreviewEnabled       bool   `json:"previewEnabled"`
	SoundEnabled         bool   `json:"soundEnabled"`
	VibrationEnabled     bool   `json:"vibrationEnabled"`
}
type PushBinding struct {
	ID             string    `json:"id"`
	Revision       int64     `json:"revision"`
	LeaseExpiresAt time.Time `json:"leaseExpiresAt,omitempty"`
}
type PushDelivery struct {
	ID              int64
	BindingID       string
	BindingRevision int64
	Request         tenancy.PushRequest
	Device          PushDevice
	// BeforeSend must be called immediately before each provider request,
	// including credential refresh/retries. It is valid only within Send.
	BeforeSend func(context.Context) error `json:"-"`
}

// Provider errors must not escape this boundary: URLs may contain device tokens.
// A provider may classify an irrecoverably invalid token without logging it.
var ErrInvalidPushDevice = errors.New("push device rejected")

type PushSender interface {
	Send(context.Context, PushDelivery) error
}
type PushService struct {
	store     *Store
	aead      cipher.AEAD
	sender    PushSender
	providers map[string]bool
	web       *webpushpolicy.Policy
}

var pushDeviceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$`)
var getuiToken = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)

func NewPushService(s *Store, key string, sender PushSender, providers []string, browser ...*webpushpolicy.Policy) (*PushService, error) {
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 || s == nil || sender == nil || len(providers) == 0 {
		return nil, tenancy.ErrInvalid
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	p := &PushService{store: s, aead: aead, sender: sender, providers: map[string]bool{}}
	if len(browser) > 1 {
		return nil, tenancy.ErrInvalid
	}
	if len(browser) == 1 {
		p.web = browser[0]
	}
	for _, provider := range providers {
		if provider != "getui" && provider != "getui_voip" && provider != "webpush" {
			return nil, tenancy.ErrInvalid
		}
		p.providers[provider] = true
	}
	if p.providers["webpush"] && (p.web == nil || p.web.PublicKey == "" || len(p.web.Hosts) == 0) {
		return nil, tenancy.ErrInvalid
	}
	return p, nil
}
func (p *PushService) validateDevice(d PushDevice) error {
	if !pushDeviceID.MatchString(d.DeviceID) || !p.providers[d.Provider] {
		return tenancy.ErrInvalid
	}
	switch d.Provider {
	case "getui":
		if (d.Platform != "android" && d.Platform != "ios") || !getuiToken.MatchString(d.PushToken) {
			return tenancy.ErrInvalid
		}
	case "getui_voip":
		if d.Platform != "ios" || !getuiToken.MatchString(d.PushToken) {
			return tenancy.ErrInvalid
		}
	case "webpush":
		if d.Platform != "web" || p.web == nil {
			return tenancy.ErrInvalid
		}
		if _, _, e := p.web.Subscription(d.PushToken); e != nil {
			return tenancy.ErrInvalid
		}
	default:
		return tenancy.ErrInvalid
	}
	return nil
}
func (p *PushService) seal(provider, token string) ([]byte, error) {
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return p.aead.Seal(nonce, nonce, []byte(token), []byte("platform-push-v1:"+provider)), nil
}
func (p *PushService) open(provider string, raw []byte) (string, error) {
	n := p.aead.NonceSize()
	if len(raw) < n {
		return "", ErrUnavailable
	}
	text, err := p.aead.Open(nil, raw[:n], raw[n:], []byte("platform-push-v1:"+provider))
	if err != nil {
		return "", ErrUnavailable
	}
	return string(text), nil
}

// Account -> tenant -> session -> device lock order also covers refresh, logout,
// transfer, password changes and disable operations. Tokens never go to tenants.
func pushSession(ctx context.Context, tx pgx.Tx, token string) (account, int64, error) {
	var a account
	if len(token) != 43 {
		return a, 0, ErrDenied
	}
	var id string
	if err := tx.QueryRow(ctx, `SELECT account_id FROM platform_sessions WHERE token_hash=$1`, tenancy.Hash(token)).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrDenied
		}
		return a, 0, err
	}
	a, err := readAccount(ctx, tx, id)
	if err != nil {
		return a, 0, err
	}
	if a.State != "active" || a.CredentialsPending || a.GloballyBlocked {
		return a, 0, ErrDenied
	}
	realm, err := realmVersion(ctx, tx, a.TenantID)
	if err != nil {
		return a, 0, err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT true FROM platform_sessions WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp() AND assignment_version=$2 AND auth_version=$3 AND realm_version=$4 FOR SHARE`, tenancy.Hash(token), a.AssignmentVersion, a.AuthVersion, realm).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrDenied
	}
	return a, realm, err
}
func (p *PushService) Bind(ctx context.Context, token string, d PushDevice) (PushBinding, error) {
	var result PushBinding
	if d.Provider == "webpush" {
		if p.web == nil {
			return result, tenancy.ErrInvalid
		}
		canonical, _, e := p.web.Subscription(d.PushToken)
		if e != nil {
			return result, tenancy.ErrInvalid
		}
		d.PushToken = canonical
	}
	if err := p.validateDevice(d); err != nil {
		return result, err
	}
	credentialHash := tenancy.Hash(d.PushToken)
	tokenHash := credentialHash
	if d.Provider == "webpush" {
		_, subscription, e := p.web.Subscription(d.PushToken)
		if e != nil {
			return result, tenancy.ErrInvalid
		}
		// A browser endpoint has one owner even when its encryption keys rotate.
		// Credential changes bump revision without creating a second recipient.
		tokenHash = tenancy.Hash(subscription.Endpoint)
	}
	encrypted, err := p.seal(d.Provider, d.PushToken)
	if err != nil {
		return result, err
	}
	tx, err := p.store.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	a, realm, err := pushSession(ctx, tx, token)
	if err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `SELECT expires_at FROM platform_sessions WHERE token_hash=$1`, tenancy.Hash(token)).Scan(&result.LeaseExpiresAt); err != nil {
		return result, err
	}
	// Only registration uses this lock. Serializing token rebinding avoids two
	// account/device uniqueness conflicts; ordinary push deliveries don't use it.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(490739176)`); err != nil {
		return result, err
	}
	if d.Provider == "getui" || d.Provider == "getui_voip" {
		// CID ownership spans both capabilities. Moving one to a new session
		// invalidates the other old-session binding before another delivery.
		if _, err = tx.Exec(ctx, `UPDATE platform_push_devices SET revoked_at=clock_timestamp(),token_cipher=''::bytea,revision=revision+1 WHERE provider IN ('getui','getui_voip') AND token_hash=$1 AND session_hash<>$2 AND revoked_at IS NULL`, tokenHash, tenancy.Hash(token)); err != nil {
			return result, err
		}
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM platform_push_devices d JOIN platform_sessions s ON s.token_hash=d.session_hash WHERE d.account_id=$1 AND d.revoked_at IS NULL AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND d.assignment_version=$2 AND d.auth_version=$3 AND d.realm_version=$4 AND NOT(d.session_hash=$5 AND d.provider=$6 AND d.device_id=$7) AND NOT(d.provider=$6 AND d.token_hash=$8)`, a.AccountID, a.AssignmentVersion, a.AuthVersion, realm, tenancy.Hash(token), d.Provider, d.DeviceID, tokenHash).Scan(&count); err != nil {
		return result, err
	}
	if count >= 20 {
		return result, tenancy.ErrInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_push_devices SET revoked_at=clock_timestamp(),token_cipher=''::bytea,revision=revision+1 WHERE session_hash=$1 AND provider=$2 AND device_id=$3 AND token_hash<>$4 AND revoked_at IS NULL`, tenancy.Hash(token), d.Provider, d.DeviceID, tokenHash); err != nil {
		return result, err
	}
	id, err := newID("push")
	if err != nil {
		return result, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO platform_push_devices(id,provider,token_hash,token_cipher,device_id,platform,account_id,tenant_id,local_user_id,assignment_version,auth_version,realm_version,session_hash,notifications_enabled,preview_enabled,sound_enabled,vibration_enabled,credential_hash)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
	 ON CONFLICT(provider,token_hash) DO UPDATE SET
	 revision=platform_push_devices.revision+CASE WHEN (platform_push_devices.session_hash,platform_push_devices.device_id,platform_push_devices.platform,platform_push_devices.notifications_enabled,platform_push_devices.preview_enabled,platform_push_devices.sound_enabled,platform_push_devices.vibration_enabled,platform_push_devices.revoked_at,platform_push_devices.credential_hash) IS DISTINCT FROM (excluded.session_hash,excluded.device_id,excluded.platform,excluded.notifications_enabled,excluded.preview_enabled,excluded.sound_enabled,excluded.vibration_enabled,excluded.revoked_at,excluded.credential_hash) THEN 1 ELSE 0 END,
	 credential_hash=excluded.credential_hash,
	 token_cipher=excluded.token_cipher,device_id=excluded.device_id,platform=excluded.platform,account_id=excluded.account_id,tenant_id=excluded.tenant_id,local_user_id=excluded.local_user_id,assignment_version=excluded.assignment_version,auth_version=excluded.auth_version,realm_version=excluded.realm_version,session_hash=excluded.session_hash,notifications_enabled=excluded.notifications_enabled,preview_enabled=excluded.preview_enabled,sound_enabled=excluded.sound_enabled,vibration_enabled=excluded.vibration_enabled,revoked_at=NULL,updated_at=clock_timestamp()
	 RETURNING id,revision`, id, d.Provider, tokenHash, encrypted, d.DeviceID, d.Platform, a.AccountID, a.TenantID, a.LocalUserID, a.AssignmentVersion, a.AuthVersion, realm, tenancy.Hash(token), d.NotificationsEnabled, d.PreviewEnabled, d.SoundEnabled, d.VibrationEnabled, credentialHash).Scan(&result.ID, &result.Revision)
	if err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,metadata) VALUES($1,'push.device.bound',$1,$2,jsonb_build_object('bindingId',$3::text,'revision',$4::bigint,'provider',$5::text))`, a.AccountID, a.TenantID, result.ID, result.Revision, d.Provider); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (p *PushService) Unbind(ctx context.Context, token, deviceID, provider string) error {
	if !pushDeviceID.MatchString(deviceID) || !p.providers[provider] {
		return tenancy.ErrInvalid
	}
	tx, err := p.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, _, err = pushSession(ctx, tx, token); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE platform_push_devices SET revoked_at=clock_timestamp(),token_cipher=''::bytea,revision=revision+1 WHERE session_hash=$1 AND device_id=$2 AND provider=$3 AND revoked_at IS NULL`, tenancy.Hash(token), deviceID, provider)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func pushAccount(ctx context.Context, tx pgx.Tx, in tenancy.PushRequest) error {
	a, err := readAccount(ctx, tx, in.AccountID)
	if err != nil {
		return err
	}
	if a.Identity != in.Identity || a.State != "active" || a.CredentialsPending || a.GloballyBlocked || a.AuthVersion != in.AuthVersion {
		return ErrDenied
	}
	realm, err := realmVersion(ctx, tx, a.TenantID)
	if err != nil {
		return err
	}
	if realm != in.RealmVersion {
		return ErrDenied
	}
	return nil
}
func (p *PushService) Deliver(ctx context.Context, tenant string, in tenancy.PushRequest) (tenancy.PushReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var result tenancy.PushReceipt
	if tenant != in.TenantID {
		return result, ErrDenied
	}
	if err := in.Validate(time.Now()); err != nil {
		return result, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return result, err
	}
	tx, err := p.store.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if err = pushAccount(ctx, tx, in); err != nil {
		return result, err
	}
	var requestID int64
	var same bool
	err = tx.QueryRow(ctx, `SELECT id,input_hash=$3 FROM platform_push_requests WHERE tenant_id=$1 AND request_id=$2`, tenant, in.RequestID, tenancy.Hash(string(raw))).Scan(&requestID, &same)
	if err == nil && !same {
		return result, ErrRequestChanged
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO platform_push_requests(tenant_id,request_id,account_id,input_hash,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING id`, tenant, in.RequestID, in.AccountID, tenancy.Hash(string(raw)), in.ExpiresAt).Scan(&requestID)
		if err != nil {
			return result, err
		}
		// Freeze recipients once. Retrying an old event must not notify a newly
		// registered device or a later owner of the same physical push token.
		_, err = tx.Exec(ctx, `INSERT INTO platform_push_deliveries(request_id,device_id,binding_revision)
		 SELECT $1,d.id,d.revision FROM platform_push_devices d JOIN platform_sessions s ON s.token_hash=d.session_hash
		 WHERE d.account_id=$2 AND d.tenant_id=$3 AND d.local_user_id=$4 AND d.assignment_version=$5 AND d.auth_version=$6 AND d.realm_version=$7 AND d.revoked_at IS NULL AND d.notifications_enabled AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND $8::timestamptz>clock_timestamp()
		 AND d.provider IN ('getui','getui_voip','webpush')
		 AND ($9='call.invited' OR d.provider<>'getui_voip')
		 AND (d.provider<>'getui_voip' OR $10)
		 AND NOT($9='call.invited' AND d.provider='getui' AND d.platform='ios')`, requestID, in.AccountID, tenant, in.LocalUserID, in.AssignmentVersion, in.AuthVersion, in.RealmVersion, in.ExpiresAt, in.EventType, p.providers["getui_voip"])
	}
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	rows, err := p.store.pool.Query(ctx, `SELECT id FROM platform_push_deliveries WHERE request_id=$1 ORDER BY id`, requestID)
	if err != nil {
		return result, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	for _, id := range ids {
		status, e := p.deliverOne(ctx, id, in)
		if e != nil {
			return result, e
		}
		if status == "sent" {
			result.Sent++
		} else {
			result.Skipped++
		}
	}
	result.Status = "completed"
	if in.EventType == "call.invited" && result.Sent == 0 {
		result.Status = "capability_not_ready"
	}
	return result, nil
}
func (p *PushService) deliverOne(ctx context.Context, id int64, in tenancy.PushRequest) (string, error) {
	tx, err := p.store.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err = pushAccount(ctx, tx, in); err != nil {
		return "", err
	}
	var status, bindingID string
	var revision int64
	err = tx.QueryRow(ctx, `SELECT status,device_id,binding_revision FROM platform_push_deliveries WHERE id=$1 FOR UPDATE`, id).Scan(&status, &bindingID, &revision)
	if err != nil {
		return "", err
	}
	if status != "pending" {
		return status, tx.Commit(ctx)
	}
	var d PushDevice
	var encrypted []byte
	var valid bool
	var sessionExpiry time.Time
	err = tx.QueryRow(ctx, `SELECT d.device_id,d.platform,d.provider,d.token_cipher,d.notifications_enabled,d.preview_enabled,d.sound_enabled,d.vibration_enabled,
	 d.account_id=$2 AND d.tenant_id=$3 AND d.local_user_id=$4 AND d.assignment_version=$5 AND d.auth_version=$6 AND d.realm_version=$7 AND d.revision=$8 AND d.revoked_at IS NULL AND d.notifications_enabled AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND $9::timestamptz>clock_timestamp(),s.expires_at
	 FROM platform_push_devices d JOIN platform_sessions s ON s.token_hash=d.session_hash WHERE d.id=$1 FOR UPDATE OF d FOR SHARE OF s`, bindingID, in.AccountID, in.TenantID, in.LocalUserID, in.AssignmentVersion, in.AuthVersion, in.RealmVersion, revision, in.ExpiresAt).Scan(&d.DeviceID, &d.Platform, &d.Provider, &encrypted, &d.NotificationsEnabled, &d.PreviewEnabled, &d.SoundEnabled, &d.VibrationEnabled, &valid, &sessionExpiry)
	if err != nil {
		return "", err
	}
	status = "skipped"
	if valid {
		if d.PushToken, err = p.open(d.Provider, encrypted); err != nil {
			return "", err
		}
		if err = p.validateDevice(d); err != nil {
			return "", ErrUnavailable
		}
		// Monitor the exact connection holding the account/realm/session/device
		// locks. A dead connection must cancel provider I/O, not merely fail a
		// later commit. External acceptance remains non-transactional/irreversible.
		until := in.ExpiresAt
		if sessionExpiry.Before(until) {
			until = sessionExpiry
		}
		err = p.sendFenced(ctx, tx, until, PushDelivery{ID: id, BindingID: bindingID, BindingRevision: revision, Request: in, Device: d})
		if errors.Is(err, ErrInvalidPushDevice) {
			status = "invalid"
			if _, err = tx.Exec(ctx, `UPDATE platform_push_devices SET revoked_at=clock_timestamp(),token_cipher=''::bytea,revision=revision+1 WHERE id=$1`, bindingID); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", ErrUnavailable
		} else {
			status = "sent"
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_push_deliveries SET status=$2,attempts=attempts+1,updated_at=clock_timestamp() WHERE id=$1`, id, status); err != nil {
		return "", err
	}
	return status, tx.Commit(ctx)
}

func (a *API) pushConfig(w http.ResponseWriter, r *http.Request) {
	providers := []string{}
	if a.Push != nil {
		for _, p := range []string{"getui", "getui_voip", "webpush"} {
			if a.Push.providers[p] {
				providers = append(providers, p)
			}
		}
	}
	result := map[string]any{"enabled": len(providers) > 0, "providers": providers, "webPushEnabled": a.Push != nil && a.Push.providers["webpush"]}
	if a.Push != nil && a.Push.providers["webpush"] {
		result["webPushPublicKey"] = a.Push.web.PublicKey
	}
	respond(w, 200, result)
}
func (a *API) bindPushDevice(w http.ResponseWriter, r *http.Request) {
	if a.Push == nil {
		failure(w, ErrUnavailable)
		return
	}
	var in struct {
		RefreshToken string     `json:"refreshToken"`
		Device       PushDevice `json:"device"`
	}
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	result, err := a.Push.Bind(r.Context(), in.RefreshToken, in.Device)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 201, result)
}
func (a *API) unbindPushDevice(w http.ResponseWriter, r *http.Request) {
	if a.Push == nil {
		failure(w, ErrUnavailable)
		return
	}
	var in struct {
		RefreshToken string `json:"refreshToken"`
		DeviceID     string `json:"deviceId"`
		Provider     string `json:"provider"`
	}
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if err := a.Push.Unbind(r.Context(), in.RefreshToken, in.DeviceID, in.Provider); err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *API) deliverPush(w http.ResponseWriter, r *http.Request) {
	identity := tenancy.PeerIdentity(r)
	tenant := strings.TrimPrefix(identity, "spiffe://frogim/tenant/")
	if tenant == identity || !tenancy.ValidID(tenant) {
		failure(w, ErrDenied)
		return
	}
	if a.Push == nil {
		failure(w, ErrUnavailable)
		return
	}
	var in tenancy.PushRequest
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if !a.allow(w, r, "tenant-push:"+tenant, 1200, time.Minute) {
		return
	}
	result, err := a.Push.Deliver(r.Context(), tenant, in)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}
