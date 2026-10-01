package tenancy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ImportReport deliberately excludes identities and credentials.
type ImportReport struct {
	Phase                                                   string `json:"phase"`
	Total, Deleted, Disabled, InvalidPhone, WithoutPassword int
	SourceSHA256                                            string `json:"sourceSHA256"`
}
type importUser struct {
	ID, Phone, Password string
	Profile             Profile
	Banned              bool
}

// ImportStandalone only adopts existing enterprise accounts; it never creates,
// edits or removes business users or their media. Both databases must be isolated
// from application writes for import/verify. Cross-database interruption is safe:
// exact matches are reused and any mismatch fails rather than overwriting it.
func ImportStandalone(ctx context.Context, source, platform *pgxpool.Pool, tenant, phase string) (ImportReport, error) {
	report := ImportReport{Phase: phase}
	if phase != "preflight" && phase != "import" && phase != "verify" {
		return report, errors.New("unknown import phase")
	}
	tx, err := source.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return report, errors.New("source snapshot unavailable")
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,phone,password_hash,name,COALESCE(handle,''),gender,signature,COALESCE(avatar_media_id,''),avatar_url,allow_search_by_handle,allow_search_by_phone,banned,created_at,deleted_at,(banned AND (banned_until IS NULL OR banned_until>now())) FROM im_users ORDER BY id`)
	if err != nil {
		return report, errors.New("source schema unavailable")
	}
	users := []importUser{}
	fingerprint := sha256.New()
	phones := map[string]bool{}
	for rows.Next() {
		var u importUser
		if err = rows.Scan(&u.ID, &u.Phone, &u.Password, &u.Profile.Name, &u.Profile.Handle, &u.Profile.Gender, &u.Profile.Signature, &u.Profile.AvatarMediaID, &u.Profile.AvatarURL, &u.Profile.SearchHandle, &u.Profile.SearchPhone, &u.Profile.Banned, &u.Profile.CreatedAt, &u.Profile.DeletedAt, &u.Banned); err != nil {
			rows.Close()
			return report, errors.New("source profile unreadable")
		}
		u.Profile.Banned = u.Banned
		report.Total++
		b, _ := json.Marshal(u)
		fingerprint.Write(b)
		fingerprint.Write([]byte{'\n'})
		if u.Profile.DeletedAt != nil {
			report.Deleted++
			continue
		}
		if phones[u.Phone] {
			rows.Close()
			return report, errors.New("duplicate source phone; manual resolution required")
		}
		phones[u.Phone] = true
		if !phonePattern.MatchString(u.Phone) {
			u.Banned = true
			report.InvalidPhone++
		}
		if u.Banned {
			report.Disabled++
		}
		if u.Password == "" {
			report.WithoutPassword++
		}
		users = append(users, u)
	}
	rows.Close()
	if rows.Err() != nil {
		return report, errors.New("source snapshot interrupted")
	}
	// Release the read snapshot before adding triggers on the same source table.
	// Holding it while requesting DDL on another connection would self-block.
	_ = tx.Rollback(ctx)
	report.SourceSHA256 = hex.EncodeToString(fingerprint.Sum(nil))
	if phase == "preflight" {
		return report, nil
	}
	if platform == nil {
		return report, errors.New("platform database required")
	}
	if phase == "import" {
		if _, err = platform.Exec(ctx, schema); err != nil {
			return report, errors.New("platform schema initialization failed")
		}
	}
	var enabled bool
	if err = platform.QueryRow(ctx, `SELECT enabled FROM lp_tenants WHERE id=$1`, tenant).Scan(&enabled); err != nil || enabled {
		return report, errors.New("import target must exist and remain disabled")
	}
	if phase == "import" {
		// Enterprise schema additions are limited to identity bookkeeping.
		if _, err = source.Exec(ctx, enterpriseSchema); err != nil {
			return report, errors.New("enterprise identity schema initialization failed")
		}
	}
	for _, u := range users {
		pr, _ := json.Marshal(u.Profile)
		if phase == "import" {
			pt, e := platform.Begin(ctx)
			if e != nil {
				return report, errors.New("platform import transaction unavailable")
			}
			_, e = pt.Exec(ctx, `INSERT INTO lp_users(id,phone,password_hash,tenant_id,revision,banned,profile,created_at) VALUES($1,$2,$3,$4,1,$5,$6,$7) ON CONFLICT DO NOTHING`, u.ID, u.Phone, u.Password, tenant, u.Banned, pr, u.Profile.CreatedAt)
			if e == nil {
				var matches bool
				e = pt.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lp_users WHERE id=$1 AND phone=$2 AND password_hash=$3 AND tenant_id=$4 AND revision=1 AND banned=$5 AND profile=$6::jsonb AND pending='' AND created_at=$7)`, u.ID, u.Phone, u.Password, tenant, u.Banned, pr, u.Profile.CreatedAt).Scan(&matches)
				if e == nil && !matches {
					e = errors.New("identity conflict")
				}
			}
			if e == nil {
				_, e = pt.Exec(ctx, `INSERT INTO lp_memberships(user_id,tenant_id,profile,profile_version,assignment_version,synced_at) VALUES($1,$2,$3,1,1,now()) ON CONFLICT DO NOTHING`, u.ID, tenant, pr)
			}
			if e == nil {
				e = pt.Commit(ctx)
			} else {
				_ = pt.Rollback(ctx)
			}
			if e != nil {
				return report, errors.New("platform import failed; existing data not overwritten")
			}
		}
		var same bool
		err = platform.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lp_users u JOIN lp_memberships m ON m.user_id=u.id AND m.tenant_id=u.tenant_id WHERE u.id=$1 AND u.phone=$2 AND u.password_hash=$3 AND u.tenant_id=$4 AND u.revision=1 AND u.banned=$5 AND u.profile=$6::jsonb AND u.pending='' AND u.created_at=$7 AND m.profile=$6::jsonb AND m.profile_version=1 AND m.assignment_version=1)`, u.ID, u.Phone, u.Password, tenant, u.Banned, pr, u.Profile.CreatedAt).Scan(&same)
		if err != nil || !same {
			return report, errors.New("platform identity conflict; manual resolution required")
		}
		if phase == "import" {
			_, err = source.Exec(ctx, `INSERT INTO lp_identity(user_id,revision,epoch,active,prepared_by,profile_version,synced_version,synced_at) VALUES($1,1,1,false,$2,1,1,now()) ON CONFLICT DO NOTHING`, u.ID, "import:"+tenant)
			if err != nil {
				return report, errors.New("enterprise mapping interrupted; rerun original import")
			}
		}
		err = source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lp_identity WHERE user_id=$1 AND revision=1 AND epoch=1 AND NOT active AND prepared_by=$2 AND profile_version=1 AND synced_version=1)`, u.ID, "import:"+tenant).Scan(&same)
		if err != nil || !same {
			return report, errors.New("enterprise mapping conflict; manual resolution required")
		}
	}
	var count int
	if phase == "import" {
		// Deleted identities remain absent from the platform, but an inactive
		// mapping lets the cutover revoke any residual IM credentials as well.
		if _, err = source.Exec(ctx, `INSERT INTO lp_identity(user_id,revision,epoch,active,prepared_by) SELECT id,1,1,false,$1 FROM im_users WHERE deleted_at IS NOT NULL ON CONFLICT DO NOTHING`, "deleted-import:"+tenant); err != nil {
			return report, errors.New("deleted identity isolation failed")
		}
	}
	err = platform.QueryRow(ctx, `SELECT count(*) FROM lp_users WHERE tenant_id=$1`, tenant).Scan(&count)
	if err != nil || count != len(users) {
		return report, errors.New("platform import total does not match source")
	}
	// Deleted accounts must never have an active mapping.
	err = source.QueryRow(ctx, `SELECT count(*) FROM lp_identity i JOIN im_users u ON u.id=i.user_id WHERE u.deleted_at IS NOT NULL AND i.active`).Scan(&count)
	if err != nil || count != 0 {
		return report, errors.New("deleted enterprise identity unexpectedly active")
	}
	return report, nil
}

// InitializeImportPlatform is used only while the new deployment is isolated.
func InitializeImportPlatform(ctx context.Context, db *pgxpool.Pool, tenant Tenant) error {
	if _, err := db.Exec(ctx, schema); err != nil {
		return errors.New("platform schema initialization failed")
	}
	services, _ := json.Marshal(tenant.Services)
	_, err := db.Exec(ctx, `INSERT INTO lp_tenants(id,name,code,enabled,is_default,services,control_url) VALUES($1,$2,$3,false,true,$4,$5) ON CONFLICT DO NOTHING`, tenant.ID, tenant.Name, tenant.Code, services, tenant.ControlURL)
	if err != nil {
		return errors.New("isolated enterprise registration failed")
	}
	var same bool
	err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lp_tenants WHERE id=$1 AND name=$2 AND code=$3 AND NOT enabled AND is_default AND services=$4::jsonb AND control_url=$5)`, tenant.ID, tenant.Name, tenant.Code, services, tenant.ControlURL).Scan(&same)
	if err != nil || !same {
		return errors.New("import enterprise configuration conflict")
	}
	return nil
}
