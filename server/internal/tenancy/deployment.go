package tenancy

import (
	"context"
	"errors"
)

// Public addresses are administrator-owned and must match the authenticated deployment.
func (p *Platform) checkDeployment(ctx context.Context, t Tenant) error {
	var ready struct {
		Status   string   `json:"status"`
		TenantID string   `json:"tenantId"`
		Services Services `json:"services"`
	}
	if e := p.control(ctx, t, "/internal/directory/ready", nil, &ready); e != nil {
		return e
	}
	if ready.Status != "ready" || ready.TenantID != t.ID || ready.Services != t.Services {
		return errors.New("enterprise deployment addresses do not match directory")
	}
	return nil
}
