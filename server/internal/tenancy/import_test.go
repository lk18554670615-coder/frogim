package tenancy

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/config"
	"golang.org/x/crypto/bcrypt"
	"os"
	"strings"
	"testing"
)

func isolatedImportDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_LIGHT_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "lp_import_" + ID()[:16]
	if _, err = base.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		base.Close()
		t.Fatal(err)
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	c.ConnConfig.Database = name
	db, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); _, _ = base.Exec(ctx, `DROP DATABASE `+name); base.Close() })
	return db
}

func TestStandaloneImport(t *testing.T) {
	source, target := isolatedImportDB(t), isolatedImportDB(t)
	ctx := context.Background()
	exec := func(db *pgxpool.Pool, sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(source, `CREATE TABLE im_users(id text PRIMARY KEY,phone text,name text,handle text,gender text,signature text,avatar_media_id text,avatar_url text,allow_search_by_handle boolean,allow_search_by_phone boolean,banned boolean,created_at timestamptz,deleted_at timestamptz,banned_until timestamptz,password_hash text); INSERT INTO im_users VALUES('normal','13844440001','甲','handle1','unspecified','签名',NULL,'',true,false,false,now(),NULL,NULL,'hash'),('invalid','bad','乙','handle2','unspecified','',NULL,'',true,false,false,now(),NULL,NULL,''),('timed','13844440003','丙','handle3','unspecified','',NULL,'',true,false,true,now(),NULL,now()+interval '1 day',''),('expired','13844440005','戊','handle5','unspecified','',NULL,'',true,false,true,now(),NULL,now()-interval '1 day','hash'),('deleted','13844440004','丁','handle4','unspecified','',NULL,'',true,false,false,now(),now(),NULL,'')`)
	tenant := Tenant{ID: "enterprise-a", Name: "客户A企业", Code: "A", ControlURL: "https://enterprise-a:8443", Services: Services{API: "https://example.com", IMWS: "wss://example.com/im", IMTCP: "tcp://example.com:5100", RTC: "wss://example.com/livekit", Media: "https://example.com"}}
	r, err := ImportStandalone(ctx, source, nil, tenant.ID, "preflight")
	if err != nil || r.Total != 5 || r.Deleted != 1 || r.InvalidPhone != 1 || r.Disabled != 2 || r.WithoutPassword != 2 {
		t.Fatal(r, err)
	}
	if err = InitializeImportPlatform(ctx, target, tenant); err != nil {
		t.Fatal(err)
	}
	// Simulate confirmation loss after the platform half committed.
	exec(target, `INSERT INTO lp_users(id,phone,password_hash,tenant_id,profile,created_at) SELECT 'normal','13844440001','hash',$1,'{}',now()`, tenant.ID)
	if _, err = ImportStandalone(ctx, source, target, tenant.ID, "import"); err == nil {
		t.Fatal("existing mismatched identity overwritten")
	}
	exec(target, `DELETE FROM lp_users WHERE id='normal'`)
	for _, phase := range []string{"import", "import", "verify"} {
		got, e := ImportStandalone(ctx, source, target, tenant.ID, phase)
		if e != nil || got.SourceSHA256 != r.SourceSHA256 {
			t.Fatal(phase, got, e)
		}
	}
	var n int
	_ = target.QueryRow(ctx, `SELECT count(*) FROM lp_users`).Scan(&n)
	if n != 4 {
		t.Fatal(n)
	}
	var expiredBanned bool
	_ = target.QueryRow(ctx, `SELECT banned FROM lp_users WHERE id='expired'`).Scan(&expiredBanned)
	if expiredBanned {
		t.Fatal("expired ban extended by import")
	}
	var same bool
	_ = target.QueryRow(ctx, `SELECT banned AND password_hash='' FROM lp_users WHERE id='invalid'`).Scan(&same)
	if !same {
		t.Fatal("invalid phone not retained disabled")
	}
	exec(source, `DELETE FROM lp_identity WHERE user_id='normal'`)
	if _, err = ImportStandalone(ctx, source, target, tenant.ID, "verify"); err == nil {
		t.Fatal("missing enterprise mapping accepted")
	}
	if _, err = ImportStandalone(ctx, source, target, tenant.ID, "import"); err != nil {
		t.Fatal("partial import not resumable", err)
	}
	exec(source, `UPDATE lp_identity SET active=true WHERE user_id='normal'`)
	if _, err = ImportStandalone(ctx, source, target, tenant.ID, "import"); err == nil {
		t.Fatal("active identity overwritten")
	}
	exec(target, `UPDATE lp_tenants SET enabled=true`)
	if _, err = ImportStandalone(ctx, source, target, tenant.ID, "import"); err == nil {
		t.Fatal("live tenant import allowed")
	}
	exec(source, `INSERT INTO im_users SELECT 'duplicate',phone,name,handle,gender,signature,avatar_media_id,avatar_url,allow_search_by_handle,allow_search_by_phone,banned,created_at,deleted_at,banned_until,password_hash FROM im_users WHERE id='normal'`)
	if _, err = ImportStandalone(ctx, source, nil, tenant.ID, "preflight"); err == nil {
		t.Fatal("duplicate source phone accepted")
	}
}

func TestProductionFixedOTP(t *testing.T) {
	db := isolatedImportDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.MinCost)
	p := &Platform{DB: db, cfg: config.Config{AdminUsername: "admin", AdminPasswordHash: string(hash), JWTSecret: strings.Repeat("x", 40)}, o: Options{FixedOTPCode: "123456"}}
	for _, purpose := range []string{"register", "login", "reset", "phone"} {
		if !p.verifyOTP(ctx, "13844440009", "123456", purpose) || p.verifyOTP(ctx, "13844440009", "000000", purpose) {
			t.Fatal(purpose)
		}
	}
	if w := testRequest(p, "POST", "/v2/auth/code", map[string]string{"phone": "13844440009"}, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	w := testRequest(p, "POST", "/admin/auth/login", map[string]string{"username": "admin", "password": "123456"}, "")
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || !w.Result().Cookies()[0].Secure || !w.Result().Cookies()[0].HttpOnly {
		t.Fatal("production login cookie", w.Code)
	}
	if err := (Options{Mode: "platform", FixedOTPCode: "bad", ControlAddr: ":8443", Certificate: "x", Key: "x", CA: "x"}).Validate(); err == nil {
		t.Fatal("invalid fixed OTP accepted")
	}
}
