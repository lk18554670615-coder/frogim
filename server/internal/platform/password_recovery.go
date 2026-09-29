package platform

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

// RecoverySMS delivers an already generated, purpose-bound code. It cannot
// verify login OTPs or return an authentication assertion to the platform.
type RecoverySMS interface {
	DeliverRecovery(context.Context, string, string, string) error
}

func validRecoveryToken(token string) bool {
	b, e := base64.RawURLEncoding.DecodeString(token)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == token
}
func recoveryCodeHash(token, code string) []byte {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte("password-reset\x00" + code))
	return m.Sum(nil)
}

func (s *Store) RequestPasswordRecovery(ctx context.Context, id, phone, token string, sms RecoverySMS) error {
	var err error
	phone, err = NormalizePhone(phone)
	if err != nil || !tenancy.ValidID(id) || !validRecoveryToken(token) || sms == nil {
		return tenancy.ErrInvalid
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if fixed, ok := sms.(*FixedOTP); ok {
		code = fixed.code
	}
	// Missing, blocked and not-yet-active accounts get the same delivery response.
	// No directory or enterprise identity is exposed before phone verification.
	tag, err := s.pool.Exec(ctx, `INSERT INTO platform_password_recovery(id,capability_hash,phone,account_id,assignment_version,auth_version,code_hash,expires_at,realm_version)
 SELECT $1,$2,$3,a.id,a.assignment_version,a.auth_version,$4,clock_timestamp()+interval '10 minutes',a.access_version
 FROM (SELECT 1) seed LEFT JOIN (SELECT a.*,t.access_version FROM platform_accounts a JOIN platform_tenants t ON t.id=a.tenant_id AND t.status='active') a ON a.phone=$3 AND a.state='active' AND NOT a.credentials_pending AND NOT a.globally_blocked
 ON CONFLICT DO NOTHING`, id, tenancy.Hash(token), phone, recoveryCodeHash(token, code))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var priorPhone, delivery string
		var priorHash []byte
		err = s.pool.QueryRow(ctx, `SELECT phone,capability_hash,delivery_status FROM platform_password_recovery WHERE id=$1 AND expires_at>clock_timestamp()`, id).Scan(&priorPhone, &priorHash, &delivery)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		if priorPhone != phone || !hmac.Equal(priorHash, tenancy.Hash(token)) {
			return ErrRequestChanged
		}
		// Never resend a code or invent success after an uncertain provider response.
		if delivery != "sent" {
			return ErrUnavailable
		}
		return nil
	}
	delivery := "sent"
	sendErr := sms.DeliverRecovery(ctx, phone, code, id)
	if sendErr != nil {
		delivery = "unconfirmed"
	}
	_, err = s.pool.Exec(ctx, `UPDATE platform_password_recovery SET delivery_status=$2 WHERE id=$1`, id, delivery)
	if err != nil {
		return err
	}
	if sendErr != nil {
		return ErrUnavailable
	}
	return nil
}

// Persist failed attempts and successful verification independently of policy
// RPCs. Verification is not consumption: consumption and task creation are one
// transaction and re-check the account/assignment/authentication snapshot.
func (s *Store) verifyRecovery(ctx context.Context, id, token, code string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var accountID string
	var expected []byte
	var attempts int
	var usable bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(account_id,''),code_hash,attempts,consumed_at IS NULL AND expires_at>clock_timestamp() FROM platform_password_recovery WHERE id=$1 AND capability_hash=$2 FOR UPDATE`, id, tenancy.Hash(token)).Scan(&accountID, &expected, &attempts, &usable)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrDenied
	}
	if err != nil {
		return "", err
	}
	if !usable || attempts >= 5 {
		return "", ErrDenied
	}
	if len(code) != 6 || !hmac.Equal(expected, recoveryCodeHash(token, code)) {
		if _, err = tx.Exec(ctx, `UPDATE platform_password_recovery SET attempts=attempts+1 WHERE id=$1`, id); err != nil {
			return "", err
		}
		if err = tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", ErrDenied
	}
	if accountID == "" {
		return "", ErrDenied
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_password_recovery SET verified_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return "", err
	}
	return accountID, tx.Commit(ctx)
}

func authorizeRecovery(ctx context.Context, tx pgx.Tx, a account, r credentialRequest) error {
	var valid bool
	// Every task, including a retry after an enterprise policy RPC, must still
	// refer to the exact account version which requested this recovery code.
	err := tx.QueryRow(ctx, `SELECT COALESCE(account_id=$3 AND phone=$4 AND assignment_version=$5 AND auth_version=$6 AND verified_at IS NOT NULL AND consumed_at IS NULL AND attempts<5 AND expires_at>clock_timestamp() AND realm_version=(SELECT access_version FROM platform_tenants WHERE id=$7 AND status='active'),false) FROM platform_password_recovery WHERE id=$1 AND capability_hash=$2 FOR UPDATE`, r.ID, tenancy.Hash(r.Token), a.AccountID, a.Phone, a.AssignmentVersion, a.AuthVersion, a.TenantID).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	if !valid {
		return ErrDenied
	}
	return nil
}

func (s *Store) RecoverPassword(ctx context.Context, id, token, code, next string, policy PasswordPolicy) (CredentialJob, error) {
	if !tenancy.ValidID(id) || !validRecoveryToken(token) || len(next) > 72 || len([]rune(next)) < 8 {
		return CredentialJob{}, tenancy.ErrInvalid
	}
	// Exact task replay does not reconsume an OTP. A different password/request
	// cannot reuse the capability after consumption.
	var accountID string
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(account_id,'') FROM platform_password_recovery WHERE id=$1 AND capability_hash=$2 AND created_at>clock_timestamp()-interval '24 hours'`, id, tenancy.Hash(token)).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CredentialJob{}, ErrDenied
	}
	if err != nil {
		return CredentialJob{}, err
	}
	if accountID == "" {
		return CredentialJob{}, ErrDenied
	}
	r := credentialRequest{ID: id, AccountID: accountID, Actor: accountID, Purpose: "reset", Reason: "verified phone password recovery", Password: next, Token: token}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CredentialJob{}, err
	}
	replay, e := credentialReplay(ctx, tx, r)
	tx.Rollback(ctx)
	if e != nil {
		return CredentialJob{}, e
	}
	if replay != nil {
		return *replay, nil
	}
	verified, err := s.verifyRecovery(ctx, id, token, code)
	if err != nil {
		// A concurrent identical submit can have consumed the challenge while
		// this request waited for its row lock. Reconcile that exact task only.
		if errors.Is(err, ErrDenied) {
			tx, e := s.pool.Begin(ctx)
			if e != nil {
				return CredentialJob{}, e
			}
			replayed, e := credentialReplay(ctx, tx, r)
			tx.Rollback(ctx)
			if e != nil {
				return CredentialJob{}, e
			}
			if replayed != nil {
				return *replayed, nil
			}
		}
		return CredentialJob{}, err
	}
	if verified != accountID {
		return CredentialJob{}, ErrDenied
	}
	return s.requestCredentials(ctx, r, policy)
}

func (s *Store) RecoveryStatus(ctx context.Context, id, token string) (CredentialJob, error) {
	j := CredentialJob{}
	if !tenancy.ValidID(id) || !validRecoveryToken(token) {
		return j, ErrDenied
	}
	var hash []byte
	err := s.pool.QueryRow(ctx, `SELECT capability_hash FROM platform_password_recovery WHERE id=$1 AND created_at>clock_timestamp()-interval '24 hours'`, id).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrDenied
	}
	if err != nil {
		return j, err
	}
	if !bytes.Equal(hash, tenancy.Hash(token)) {
		return j, ErrDenied
	}
	err = s.pool.QueryRow(ctx, `SELECT id,request_id,state,error_code FROM platform_credential_jobs WHERE request_id=$1 AND purpose='reset' AND requester_hash=$2`, id, hash).Scan(&j.ID, &j.RequestID, &j.Status, &j.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return CredentialJob{RequestID: id, Status: "unconfirmed"}, nil
	}
	return j, err
}
