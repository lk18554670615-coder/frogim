package redisbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestSnapshotValidation(t *testing.T) {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	_ = e.Encode(Header{1, 1})
	r := Record{Key: []byte{0, 255}, Value: []byte{0, 255, 10}, ExpiresAt: -1}
	_ = e.Encode(r)
	valid := buf.String() + `{"end":true,"count":1}`
	if Read(strings.NewReader(valid), nil) != nil {
		t.Fatal("binary stream rejected")
	}
	for _, bad := range []string{buf.String(), valid + `{}`, strings.Replace(valid, `"count":1`, `"count":2`, 1), strings.Replace(valid, `"format":1`, `"format":2`, 1), strings.Replace(valid, `"database":1`, `"database":16`, 1)} {
		if Read(strings.NewReader(bad), nil) == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
	_ = e.Encode(r)
	if Read(strings.NewReader(buf.String()+`{"end":true,"count":2}`), nil) == nil {
		t.Fatal("duplicate accepted")
	}
}

// Run ONLY inside a fresh disposable Redis server. Never enabled by local app startup.
func TestRedisLogicalDatabaseRoundTrip(t *testing.T) {
	raw := os.Getenv("REDIS_BACKUP_TEST_URL")
	if raw == "" {
		t.Skip("disposable Redis URL required")
	}
	src, err := Open(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if src.Options().DB != 1 {
		t.Fatal("fixture must select DB1")
	}
	opts := *src.Options()
	opts.DB = 0
	platform := redis.NewClient(&opts)
	defer platform.Close()
	opts.DB = 2
	dst := redis.NewClient(&opts)
	defer dst.Close()
	ctx := context.Background()
	for _, c := range []*redis.Client{platform, src, dst} {
		n, e := c.DBSize(ctx).Result()
		if e != nil || n != 0 {
			t.Fatal("fixture not fresh")
		}
	}
	if platform.Set(ctx, "same-key", "platform", 0).Err() != nil || src.Set(ctx, "same-key", "enterprise", 0).Err() != nil || src.Set(ctx, "\x00\xff", "\xff\x00value", 0).Err() != nil || src.HSet(ctx, "hash", "f", "v").Err() != nil || src.Set(ctx, "expires", "ttl", time.Minute).Err() != nil {
		t.Fatal("fixture seed")
	}
	expiry, err := src.PExpireTime(ctx, "expires").Result()
	if err != nil {
		t.Fatal(err)
	}
	var snapshot bytes.Buffer
	if Export(ctx, src, &snapshot) != nil || Import(ctx, dst, bytes.NewReader(snapshot.Bytes())) != nil {
		t.Fatal("round trip")
	}
	if platform.Get(ctx, "same-key").Val() != "platform" || src.Get(ctx, "same-key").Val() != "enterprise" || dst.Get(ctx, "\x00\xff").Val() != "\xff\x00value" || dst.HGet(ctx, "hash", "f").Val() != "v" || dst.PExpireTime(ctx, "expires").Val() != expiry {
		t.Fatal("DB/bytes/expiry changed")
	}
	if Import(ctx, dst, bytes.NewReader(snapshot.Bytes())) == nil {
		t.Fatal("nonempty target overwritten")
	}
	t.Log("DB1 binary values/hash/absolute expiry preserved in DB2; platform DB0 untouched; nonempty target refused")
}
