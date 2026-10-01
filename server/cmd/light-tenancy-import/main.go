package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	phase := flag.String("phase", "preflight", "preflight, import or verify")
	configFile := flag.String("config", "", "private JSON: sourceDatabaseUrl, platformDatabaseUrl, tenant")
	hashInput := flag.Bool("hash-password-stdin", false, "hash a password supplied on stdin; private setup only")
	checkConfig := flag.Bool("check-config", false, "validate mounted production configuration without starting workers")
	flag.Parse()
	if *checkConfig {
		o := tenancy.LoadOptions()
		if o.Validate() != nil {
			fatal("invalid tenancy configuration")
		}
		if _, e := tenancy.ControlClient(o); e != nil {
			fatal("invalid mounted control certificates")
		}
		c := config.Load()
		if o.Mode == "enterprise" {
			c.PlatformAuthentication = true
			if e := c.Validate(); e != nil {
				fatal(e.Error())
			}
		}
		if o.Mode == "platform" && (len(c.JWTSecret) < 32 || c.AdminUsername == "" || c.AdminPasswordHash == "" || c.DatabaseURL == "" || c.DevMode || o.FixedOTPCode == "" || c.OTPWebhookURL != "" || c.OTPWebhookToken != "") {
			fatal("invalid production platform configuration")
		}
		fmt.Println("production configuration and control certificates passed")
		return
	}
	if *hashInput {
		b, e := io.ReadAll(io.LimitReader(os.Stdin, 73))
		if e != nil {
			fatal("password input unavailable")
		}
		password := strings.TrimSpace(string(b))
		clear(b)
		if len(password) < 6 || len(password) > 72 {
			fatal("password must be 6 to 72 bytes")
		}
		h, e := bcrypt.GenerateFromPassword([]byte(password), 12)
		if e != nil {
			fatal("password hash failed")
		}
		fmt.Print(string(h))
		return
	}
	var cfg struct {
		SourceDatabaseURL   string         `json:"sourceDatabaseUrl"`
		PlatformDatabaseURL string         `json:"platformDatabaseUrl"`
		Tenant              tenancy.Tenant `json:"tenant"`
	}
	b, err := os.ReadFile(*configFile)
	if err != nil {
		fatal("private import configuration unavailable")
	}
	if json.Unmarshal(b, &cfg) != nil {
		fatal("invalid private import configuration")
	}
	clear(b)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	source, err := pgxpool.New(ctx, cfg.SourceDatabaseURL)
	if err != nil {
		fatal("source connection configuration invalid")
	}
	defer source.Close()
	var target *pgxpool.Pool
	if *phase == "revoke" {
		client, e := tenancy.ControlClient(tenancy.LoadOptions())
		if e != nil {
			fatal("private control credentials unavailable")
		}
		rows, e := source.Query(ctx, `SELECT id FROM im_users ORDER BY id`)
		if e != nil {
			fatal("revocation inventory unavailable")
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil {
				fatal("revocation inventory unreadable")
			}
			ids = append(ids, id)
		}
		rows.Close()
		if rows.Err() != nil {
			fatal("revocation inventory interrupted")
		}
		for _, id := range ids {
			body, _ := json.Marshal(map[string]any{"userId": id, "revision": 1, "operationId": "cutover:" + cfg.Tenant.ID + ":" + id})
			request, e := http.NewRequestWithContext(ctx, "POST", cfg.Tenant.ControlURL+"/internal/directory/offline", bytes.NewReader(body))
			if e != nil {
				fatal("private revocation endpoint invalid")
			}
			request.Header.Set("Content-Type", "application/json")
			response, e := client.Do(request)
			if e != nil {
				fatal("revocation confirmation lost; retry original operation")
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				fatal("revocation not confirmed; retry original operation")
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"phase": "revoked", "accounts": len(ids)})
		return
	}
	if *phase != "preflight" {
		target, err = pgxpool.New(ctx, cfg.PlatformDatabaseURL)
		if err != nil {
			fatal("platform connection configuration invalid")
		}
		defer target.Close()
		if *phase == "import" {
			if err = tenancy.InitializeImportPlatform(ctx, target, cfg.Tenant); err != nil {
				fatal(err.Error())
			}
		}
	}
	report, err := tenancy.ImportStandalone(ctx, source, target, cfg.Tenant.ID, *phase)
	if err != nil {
		fatal(err.Error())
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
