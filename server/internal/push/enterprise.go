package push

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/store"
)

type EnterpriseProvider struct {
	Provider Provider
	DB       *pgxpool.Pool
	TenantID string
}

func (p EnterpriseProvider) Send(ctx context.Context, item store.OutboxItem) error {
	var active bool
	var epoch int64
	if err := p.DB.QueryRow(ctx, `SELECT active,epoch FROM lp_identity WHERE user_id=$1`, item.UserID).Scan(&active, &epoch); err != nil {
		return retryableDeliveryError(errors.New("enterprise push authorization unavailable"))
	}
	expected, ok := item.Payload["enterpriseEpoch"].(float64)
	if !ok {
		if n, yes := item.Payload["enterpriseEpoch"].(int64); yes {
			expected = float64(n)
			ok = true
		}
	}
	if !active || !ok || int64(expected) != epoch || item.Payload["tenantId"] != p.TenantID {
		return permanentDeliveryError(errors.New("enterprise push session expired"))
	}
	return p.Provider.Send(ctx, item)
}
