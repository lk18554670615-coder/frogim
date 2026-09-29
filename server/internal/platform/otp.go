package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

// WebhookOTP is the only runtime OTP provider. There is intentionally no
// development/fixed code fallback in the platform executable.
type WebhookOTP struct {
	url, token string
	client     *http.Client
}

func NewWebhookOTP(base, token string) (*WebhookOTP, error) {
	if tenancy.ValidateBaseURL(base, false) != nil || len(token) < 32 {
		return nil, tenancy.ErrInvalid
	}
	return &WebhookOTP{url: strings.TrimRight(base, "/"), token: token, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (p *WebhookOTP) Request(ctx context.Context, phone string) error {
	return p.call(ctx, "request", phone, "")
}
func (p *WebhookOTP) Verify(ctx context.Context, phone, code string) error {
	return p.call(ctx, "verify", phone, code)
}
func (p *WebhookOTP) call(ctx context.Context, action, phone, code string) error {
	body, _ := json.Marshal(map[string]string{"phone": phone, "code": code, "purpose": "login"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/"+action, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 422 {
		return ErrDenied
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("OTP provider unavailable")
	}
	return nil
}
