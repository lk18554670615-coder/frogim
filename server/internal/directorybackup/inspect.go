package directorybackup

import (
	"context"

	"github.com/linli/im/server/internal/tenancy"
)

type Identity struct {
	DirectoryID   string `json:"directoryId"`
	SchemaVersion int    `json:"schemaVersion"`
}

// Inspect returns no accounts, credentials, configurations or supplier data.
// Operators save this identity independently from the archive being restored.
func (d Database) Inspect(ctx context.Context) (Identity, error) {
	var identity Identity
	conn, e := d.connect(ctx)
	if e != nil {
		return identity, e
	}
	defer conn.Close(context.Background())
	var quarantined bool
	if e = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, tenancy.PlatformRecoverySchema).Scan(&quarantined); e != nil {
		return identity, ErrUnconfirmed
	}
	if quarantined {
		var activated bool
		if conn.QueryRow(ctx, `SELECT phase='activated' AND EXISTS(SELECT 1 FROM frogim_recovery.activations WHERE id=activation_id AND state='activated') FROM frogim_recovery.guard WHERE singleton`).Scan(&activated) != nil || !activated {
			return identity, ErrQuarantined
		}
	}
	e = conn.QueryRow(ctx, `SELECT directory_id::text,(SELECT MAX(version) FROM public.platform_schema_migrations) FROM public.platform_directory_identity WHERE singleton`).Scan(&identity.DirectoryID, &identity.SchemaVersion)
	if e != nil {
		return Identity{}, ErrUnconfirmed
	}
	return identity, nil
}
