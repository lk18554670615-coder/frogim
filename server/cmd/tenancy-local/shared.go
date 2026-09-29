package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/linli/im/server/internal/deployment"
)

func readLocalEnv(root, name string) (map[string]string, error) {
	b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if e != nil {
		return nil, e
	}
	result := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || result[k] != "" {
			return nil, errors.New("invalid local environment file")
		}
		result[k] = v
	}
	return result, nil
}
func ensureSharedFile(root, name string, data []byte) error {
	path := filepath.Join(root, filepath.FromSlash(name))
	if existing, e := os.ReadFile(path); e == nil {
		if !bytes.Equal(existing, data) {
			return errors.New("shared configuration changed; existing files preserved")
		}
		return nil
	}
	return save(root, name, data)
}
func prepareShared(root string) error {
	p, e := readLocalEnv(root, "platform/api.env")
	if e != nil {
		return e
	}
	t, e := readLocalEnv(root, "enterprise/api.env")
	if e != nil {
		return e
	}
	if p["PLATFORM_ENV"] != "development" || t["IM_ENV"] != "development" || t["IM_TENANT_ID"] != "default" || t["IM_TENANCY_PREVIEW"] != "true" {
		return errors.New("shared local setup only accepts the default preview")
	}
	pdb, e := url.Parse(p["PLATFORM_DATABASE_URL"])
	if e != nil {
		return e
	}
	tdb, e := url.Parse(t["IM_DATABASE_URL"])
	if e != nil {
		return e
	}
	if pdb.Host != "platform-db:5432" || tdb.Host != "enterprise-db:5432" || pdb.User == nil || tdb.User == nil {
		return errors.New("unexpected local source database")
	}
	pp, _ := pdb.User.Password()
	tp, _ := tdb.User.Password()
	var c deployment.SharedHostConfig
	path := filepath.Join(root, "shared", "config.json")
	if raw, e := os.ReadFile(path); e == nil {
		if json.Unmarshal(raw, &c) != nil || c.PlatformDatabaseSecret != pp || c.EnterpriseDatabaseSecret != tp || c.ServerID != "default-local" {
			return errors.New("shared configuration does not match the preserved source")
		}
	} else {
		c = deployment.SharedHostConfig{ServerID: "default-local", AdminSecret: secret(), PlatformDatabaseSecret: pp, EnterpriseDatabaseSecret: tp, RedisSecret: secret()}
		if e = jsonFile(root, "shared/config.json", c); e != nil {
			return e
		}
	}
	raw, e := deployment.BuildSharedDatastoreBundle(c)
	if e != nil {
		return e
	}
	if e = ensureSharedFile(root, "shared/compose.json", raw); e != nil {
		return e
	}
	for scope, settings := range map[string]map[string]string{"platform": p, "enterprise": t} {
		prefix, db, index := "PLATFORM", "platform", "0"
		if scope == "enterprise" {
			prefix, db, index = "IM", "enterprise", "1"
		}
		password := pp
		if scope == "enterprise" {
			password = tp
		}
		values := map[string]string{prefix + "_DATASTORE_MODE": "shared_host", prefix + "_DATABASE_URL": "postgres://" + scope + ":" + password + "@shared-postgres:5432/" + db + "?sslmode=disable", prefix + "_REDIS_URL": "redis://:" + c.RedisSecret + "@shared-redis:6379/" + index}
		var env strings.Builder
		for _, key := range []string{prefix + "_DATASTORE_MODE", prefix + "_DATABASE_URL", prefix + "_REDIS_URL"} {
			env.WriteString(key + "=" + values[key] + "\n")
		}
		if e = ensureSharedFile(root, "shared/"+scope+".env", []byte(env.String())); e != nil {
			return e
		}
		if e = ensureSharedFile(root, "shared/"+scope+"-source-redis.env", []byte("REDIS_DATABASE_URL="+settings[prefix+"_REDIS_URL"]+"\n")); e != nil {
			return e
		}
		if e = ensureSharedFile(root, "shared/"+scope+"-target-redis.env", []byte("REDIS_DATABASE_URL="+values[prefix+"_REDIS_URL"]+"\n")); e != nil {
			return e
		}
	}
	return ensureSharedFile(root, "shared/prepared.json", []byte("{\"version\":1,\"tenantId\":\"default\",\"serverId\":\"default-local\"}\n"))
}
