package push

import (
	"context"
	"errors"
	"time"

	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
)

type controlCaller interface {
	Call(context.Context, string, any, any) error
}

// TenantProxy only sends the immutable routing contract through the private
// authenticated platform peer. It never serializes the enterprise OutboxItem.
type TenantProxy struct {
	TenantID string
	Peer     controlCaller
}

// Platform resolves devices itself. No local device row or fake token is needed.
func (TenantProxy) usesPlatformDevices() {}

func (p TenantProxy) Send(ctx context.Context, item store.OutboxItem) error {
	if p.Peer == nil || !item.ValidTenantPush() || item.TenantPush.TenantID != p.TenantID {
		return permanentDeliveryError(errors.New("missing or invalid enterprise push provenance"))
	}
	if !item.TenantPush.ExpiresAt.After(time.Now()) {
		return nil
	}
	var receipt tenancy.PushReceipt
	if err := p.Peer.Call(ctx, "/internal/tenancy/push/deliver", *item.TenantPush, &receipt); err != nil {
		var rejected *tenancy.PushRejected
		if errors.As(err, &rejected) {
			return permanentDeliveryError(rejected)
		}
		// No remote error body, signed URL, credential or message content in logs.
		return retryableDeliveryError(errors.New("platform push temporarily unavailable"))
	}
	if receipt.Status != "completed" || receipt.Sent < 0 || receipt.Skipped < 0 {
		return retryableDeliveryError(errors.New("platform push result unconfirmed"))
	}
	return nil
}
