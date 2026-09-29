// Package redisbackup handles one logical Redis database. It never flushes a
// database, changes server configuration, or reads another database number.
// Callers must stop the selected database's writers while taking a snapshot.
package redisbackup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrSnapshot = errors.New("redis database snapshot invalid or target is not empty")

type Header struct {
	Format   int `json:"format"`
	Database int `json:"database"`
}
type Record struct {
	Key       []byte `json:"key"`
	Value     []byte `json:"value"`
	ExpiresAt int64  `json:"expiresAt"`
	End       bool   `json:"end,omitempty"`
	Count     int    `json:"count,omitempty"`
}

var capture = redis.NewScript(`local v=redis.call('DUMP',KEYS[1]); if not v then return {} end; return {v,redis.call('PEXPIRETIME',KEYS[1])}`)

func Export(ctx context.Context, client *redis.Client, out io.Writer) error {
	if client.Options().DB < 0 || client.Options().DB > 15 {
		return ErrSnapshot
	}
	keys := map[string]bool{}
	var cursor uint64
	for {
		batch, next, err := client.Scan(ctx, cursor, "*", 1000).Result()
		if err != nil {
			return err
		}
		for _, key := range batch {
			keys[key] = true
		}
		if len(keys) > 1000000 {
			return ErrSnapshot
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	w := bufio.NewWriter(out)
	enc := json.NewEncoder(w)
	if err := enc.Encode(Header{1, client.Options().DB}); err != nil {
		return err
	}
	count := 0
	for _, key := range ordered {
		values, err := capture.Run(ctx, client, []string{key}).Slice()
		if err != nil {
			return err
		}
		if len(values) == 0 {
			continue
		}
		if len(values) != 2 {
			return ErrSnapshot
		}
		value, ok := values[0].(string)
		expires, valid := values[1].(int64)
		if !ok || !valid || expires < -1 {
			return ErrSnapshot
		}
		if err = enc.Encode(Record{Key: []byte(key), Value: []byte(value), ExpiresAt: expires}); err != nil {
			return err
		}
		count++
	}
	if err := enc.Encode(Record{End: true, Count: count}); err != nil {
		return err
	}
	return w.Flush()
}

// Read validates the complete stream, including a terminal count and EOF.
// The archive should be authenticated and validated before Import is called.
func Read(in io.Reader, visit func(Record) error) error {
	dec := json.NewDecoder(in)
	dec.DisallowUnknownFields()
	var header Header
	if dec.Decode(&header) != nil || header.Format != 1 || header.Database < 0 || header.Database > 15 {
		return ErrSnapshot
	}
	seen := map[string]bool{}
	for {
		var record Record
		if dec.Decode(&record) != nil {
			return ErrSnapshot
		}
		if record.End {
			if record.Count != len(seen) || len(record.Key) != 0 || len(record.Value) != 0 || record.ExpiresAt != 0 || dec.Decode(new(any)) != io.EOF {
				return ErrSnapshot
			}
			return nil
		}
		if seen[string(record.Key)] || len(seen) >= 1000000 || len(record.Value) == 0 || record.Count != 0 || record.ExpiresAt < -1 {
			return ErrSnapshot
		}
		seen[string(record.Key)] = true
		if visit != nil {
			if err := visit(record); err != nil {
				return err
			}
		}
	}
}

func Import(ctx context.Context, client *redis.Client, in io.ReadSeeker) error {
	if client.Options().DB < 0 || client.Options().DB > 15 {
		return ErrSnapshot
	}
	if err := Read(in, nil); err != nil {
		return err
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	size, err := client.DBSize(ctx).Result()
	if err != nil {
		return err
	}
	if size != 0 {
		return ErrSnapshot
	}
	// Use the server clock. ABSTTL preserves the original expiry rather than
	// extending sessions, locks, or credentials by the duration of the outage.
	now, err := client.Time(ctx).Result()
	if err != nil {
		return err
	}
	return Read(in, func(r Record) error {
		if r.ExpiresAt >= 0 && r.ExpiresAt <= now.UnixMilli() {
			return nil
		}
		ttl := "0"
		args := []any{"RESTORE", string(r.Key), ttl, string(r.Value)}
		if r.ExpiresAt >= 0 {
			args[2] = strconv.FormatInt(r.ExpiresAt, 10)
			args = append(args, "ABSTTL")
		}
		// No REPLACE: a concurrent writer or a partial prior restore is an error.
		return client.Do(ctx, args...).Err()
	})
}

func Open(raw string) (*redis.Client, error) {
	opts, err := redis.ParseURL(raw)
	if err != nil || opts.DB < 0 || opts.DB > 15 {
		return nil, ErrSnapshot
	}
	opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout = 5*time.Second, 30*time.Second, 30*time.Second
	return redis.NewClient(opts), nil
}
