package platform

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/linli/im/server/internal/netutil"
	"github.com/linli/im/server/internal/tenancy"
)

// A separate, explicitly configured real SMS delivery integration. Never reuse
// a login verifier which may ignore a new purpose or accept a development OTP.
type WebhookRecoverySMS struct {
	url, token string
	client     *http.Client
}

func NewWebhookRecoverySMS(endpoint, token string) (*WebhookRecoverySMS, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(token) < 32 {
		return nil, tenancy.ErrInvalid
	}
	return &WebhookRecoverySMS{url: endpoint, token: token, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (s *WebhookRecoverySMS) DeliverRecovery(ctx context.Context, phone, code, id string) error {
	body, _ := json.Marshal(map[string]string{"phone": phone, "code": code, "purpose": "password_reset", "requestId": id})
	r, err := http.NewRequestWithContext(ctx, "POST", s.url, bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+s.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", id)
	response, err := s.client.Do(r)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrUnavailable
	}
	return nil
}
func (a *API) recoveryCode(w http.ResponseWriter, r *http.Request) {
	if a.RecoverySMS == nil {
		failure(w, ErrUnavailable)
		return
	}
	var p struct{ RequestID, Phone, QueryToken string }
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	phone, err := NormalizePhone(p.Phone)
	if err != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	// Independent SMS budget, in addition to the public API's global limit.
	// Only the authenticated, header-replacing private gateway is trusted.
	ip := netutil.GatewayClientIP(r, a.GatewaySecret)
	if !a.allow(w, r, "recovery-sms-ip:"+ip, 10, 10*time.Minute) {
		return
	}
	if !a.allow(w, r, "recovery-sms:"+phone, 3, 10*time.Minute) {
		return
	}
	if err = a.Store.RequestPasswordRecovery(r.Context(), p.RequestID, phone, p.QueryToken, a.RecoverySMS); err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"requestId": p.RequestID, "ok": true})
}
func (a *API) recoverPassword(w http.ResponseWriter, r *http.Request) {
	if a.RecoverySMS == nil {
		failure(w, ErrUnavailable)
		return
	}
	var p struct {
		RequestID, Code, NewPassword string
		Confirmed                    bool
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if readJSON(w, r, &p) != nil || !validRecoveryToken(token) || !p.Confirmed {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if !a.allow(w, r, "recovery-verify:"+hex.EncodeToString(tenancy.Hash(token)), 10, 10*time.Minute) {
		return
	}
	j, err := a.Store.RecoverPassword(r.Context(), p.RequestID, token, p.Code, p.NewPassword, a.Peers)
	if errors.Is(err, ErrDenied) {
		respond(w, 401, map[string]any{"error": map[string]string{"code": "PASSWORD_RECOVERY_REJECTED", "message": "验证码不可用或账号暂不可重置，请重新获取验证码或联系管理员"}})
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 202, j)
}
func (a *API) recoveryStatus(w http.ResponseWriter, r *http.Request) {
	var p struct{ RequestID string }
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	j, err := a.Store.RecoveryStatus(r.Context(), p.RequestID, token)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, j)
}
