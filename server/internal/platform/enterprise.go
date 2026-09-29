package platform

import (
	"context"
	"errors"

	"github.com/linli/im/server/internal/tenancy"
)

type EnterpriseRPC struct{ Peers map[string]*tenancy.RPC }
type IdentityOperation struct {
	OperationID string           `json:"operationId"`
	Identity    tenancy.Identity `json:"identity"`
	Input       ProvisionInput   `json:"input"`
}
type IdentityAck struct {
	OperationID string           `json:"operationId"`
	Identity    tenancy.Identity `json:"identity"`
	State       string           `json:"state"`
}

func (e EnterpriseRPC) call(ctx context.Context, operation string, i tenancy.Identity, input ProvisionInput, action, state string) error {
	peer := e.Peers[i.TenantID]
	if peer == nil {
		return ErrUnavailable
	}
	var ack IdentityAck
	if err := peer.Call(ctx, "/internal/tenancy/identities/"+action, IdentityOperation{OperationID: operation, Identity: i, Input: input}, &ack); err != nil {
		return err
	}
	if ack.OperationID != operation || ack.Identity != i || ack.State != state {
		return errors.New("enterprise acknowledgement does not match operation")
	}
	return nil
}
func (e EnterpriseRPC) PrepareIdentity(ctx context.Context, op string, i tenancy.Identity, input ProvisionInput) error {
	return e.call(ctx, op, i, input, "prepare", "prepared")
}
func (e EnterpriseRPC) RevokeIdentity(ctx context.Context, op string, i tenancy.Identity) error {
	return e.call(ctx, op, i, ProvisionInput{}, "revoke", "revoked")
}

func (e EnterpriseRPC) CheckTransfer(ctx context.Context, i tenancy.Identity) error {
	return e.call(ctx, "preflight", i, ProvisionInput{}, "check-transfer", "eligible")
}

func (e EnterpriseRPC) CheckPassword(ctx context.Context, i tenancy.Identity, runes int) error {
	peer := e.Peers[i.TenantID]
	if peer == nil {
		return ErrUnavailable
	}
	var ack struct {
		Identity tenancy.Identity `json:"identity"`
		Accepted bool             `json:"accepted"`
	}
	if err := peer.Call(ctx, "/internal/tenancy/credentials/check-password", struct {
		Identity  tenancy.Identity `json:"identity"`
		RuneCount int              `json:"runeCount"`
	}{i, runes}, &ack); err != nil {
		return err
	}
	if ack.Identity != i || !ack.Accepted {
		return ErrUnavailable
	}
	return nil
}

func (e EnterpriseRPC) RevokeCredentials(ctx context.Context, op tenancy.CredentialOperation) error {
	peer := e.Peers[op.Identity.TenantID]
	if peer == nil {
		return ErrUnavailable
	}
	var ack tenancy.CredentialAck
	if err := peer.Call(ctx, "/internal/tenancy/credentials/revoke", op, &ack); err != nil {
		return err
	}
	if ack.CredentialOperation != op || ack.State != "completed" {
		return ErrUnavailable
	}
	return nil
}

func (e EnterpriseRPC) SetAccess(ctx context.Context, op tenancy.AccessOperation) error {
	peer := e.Peers[op.Identity.TenantID]
	if peer == nil {
		return ErrUnavailable
	}
	var ack tenancy.AccessAck
	if err := peer.Call(ctx, "/internal/tenancy/access", op, &ack); err != nil {
		return err
	}
	if ack.AccessOperation != op || ack.State != "completed" {
		return ErrUnavailable
	}
	return nil
}

func (e EnterpriseRPC) SetRealm(ctx context.Context, op tenancy.RealmOperation) (tenancy.RealmAck, error) {
	peer := e.Peers[op.TenantID]
	var ack tenancy.RealmAck
	if peer == nil {
		return ack, ErrUnavailable
	}
	if err := peer.Call(ctx, "/internal/tenancy/realm", op, &ack); err != nil {
		return ack, err
	}
	if ack.RealmOperation != op || (ack.State != "completed" && ack.State != "pending") || ack.Remaining < 0 || (ack.State == "completed" && ack.Remaining != 0) || (ack.State == "pending" && ack.Remaining == 0) {
		return tenancy.RealmAck{}, ErrUnavailable
	}
	return ack, nil
}
