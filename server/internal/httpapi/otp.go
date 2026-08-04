package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var errInvalidOTP = errors.New("invalid or expired verification code")

type otpProvider interface {
	Request(context.Context, string) error
	Verify(context.Context, string, string) error
}

type devOTP string

func (d devOTP) Request(context.Context, string) error { return nil }
func (d devOTP) Verify(_ context.Context, _ string, code string) error {
	if subtle.ConstantTimeCompare([]byte(code), []byte(string(d))) != 1 {
		return errInvalidOTP
	}
	return nil
}

type webhookOTP struct {
	url, token string
	client     *http.Client
}

func newWebhookOTP(url, token string) webhookOTP {
	return webhookOTP{url: url, token: token, client: &http.Client{Timeout: 10 * time.Second}}
}
func (w webhookOTP) Request(ctx context.Context, phone string) error {
	return w.call(ctx, "request", map[string]string{"phone": phone, "purpose": "login"})
}
func (w webhookOTP) Verify(ctx context.Context, phone, code string) error {
	return w.call(ctx, "verify", map[string]string{"phone": phone, "code": code, "purpose": "login"})
}
func (w webhookOTP) call(ctx context.Context, action string, payload map[string]string) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url+"/"+action, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusUnprocessableEntity {
		return errInvalidOTP
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("OTP gateway status %d", res.StatusCode)
	}
	return nil
}
