package platform

import (
	"context"
	"crypto/subtle"
	"github.com/linli/im/server/internal/tenancy"
)

// FixedOTP implements the operator-selected development verification behavior
// for registration, OTP login and password recovery. It sends no real SMS.
type FixedOTP struct{ code string }

func NewFixedOTP(code string) (*FixedOTP, error) {
	if code == "" || !tenancy.ValidFixedVerificationCode(code) {
		return nil, tenancy.ErrInvalid
	}
	return &FixedOTP{code: code}, nil
}
func (p *FixedOTP) Request(context.Context, string) error { return nil }
func (p *FixedOTP) Verify(_ context.Context, _ string, code string) error {
	if subtle.ConstantTimeCompare([]byte(p.code), []byte(code)) != 1 {
		return ErrDenied
	}
	return nil
}
func (p *FixedOTP) DeliverRecovery(ctx context.Context, phone, code, _ string) error {
	return p.Verify(ctx, phone, code)
}
