package tenancy

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const SharedDatastoreMode = "shared_host"
const SharedDataProject = "frogim-shared-default"
const SharedDataNetwork = SharedDataProject + "_data"

// Shared data is a deliberate exception for the platform and DEFAULT tenant on
// one host. Remote tenants cannot opt into its connection profile.
func ValidSharedDatabase(name string) bool {
	return name == "enterprise" || regexp.MustCompile(`^enterprise_r_[a-z0-9_]{1,24}$`).MatchString(name)
}

func DeploymentDatastores(database, cache, role, tenant, mode string) error {
	if mode == "" || mode == "isolated" {
		return ProductionDatastores(database, cache, role+"-db", role+"-redis")
	}
	if mode != SharedDatastoreMode || (role != "platform" && role != "enterprise") || (role == "enterprise" && tenant != "default") {
		return ErrInvalid
	}
	db, e := url.Parse(database)
	r, err := url.Parse(cache)
	if e != nil || err != nil || db.User == nil || r.User == nil {
		return ErrInvalid
	}
	dbPassword, ok := db.User.Password()
	password, hasPassword := r.User.Password()
	q, qe := url.ParseQuery(db.RawQuery)
	if (db.Scheme != "postgres" && db.Scheme != "postgresql") || db.Host != "shared-postgres:5432" || db.User.Username() != role || db.Fragment != "" || !ok || len(dbPassword) < 32 || qe != nil || len(q) != 1 || len(q["sslmode"]) != 1 || (q.Get("sslmode") != "disable" && q.Get("sslmode") != "verify-full") {
		return ErrInvalid
	}
	if r.Scheme != "redis" || r.Host != "shared-redis:6379" || r.User.Username() != "" || !hasPassword || len(password) < 32 || password == dbPassword || r.RawQuery != "" || r.Fragment != "" {
		return ErrInvalid
	}
	index, e := strconv.Atoi(strings.TrimPrefix(r.Path, "/"))
	if e != nil || r.Path != "/"+strconv.Itoa(index) {
		return ErrInvalid
	}
	if role == "platform" {
		if db.Path != "/platform" || index != 0 {
			return ErrInvalid
		}
	} else if !ValidSharedDatabase(strings.TrimPrefix(db.Path, "/")) || index < 1 || index > 15 {
		return ErrInvalid
	}
	return nil
}
