package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

// GroupMessageRateStatus is scoped to the requesting member. It never exposes
// another member's activity.
type GroupMessageRateStatus struct {
	LimitPerMinute    int `json:"limitPerMinute"`
	Used              int `json:"used"`
	Remaining         int `json:"remaining"`
	RetryAfterSeconds int `json:"retryAfterSeconds"`
}

type GroupMessageRateLimiterStore interface {
	ConsumeGroupMessageRate(context.Context, string, string, int64, int, time.Duration) (GroupMessageRateStatus, error)
	GetGroupMessageRateStatus(context.Context, string, string, int64, int, time.Duration) (GroupMessageRateStatus, error)
}

var consumeGroupMessageRateScript = redis.NewScript(`
local now_parts = redis.call('TIME')
local now_ms = tonumber(now_parts[1]) * 1000 + math.floor(tonumber(now_parts[2]) / 1000)
local window_ms = tonumber(ARGV[1])
local maximum = tonumber(ARGV[2])
local cutoff = now_ms - window_ms
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', cutoff)
local used = redis.call('ZCARD', KEYS[1])
if used >= maximum then
  local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
  local retry_ms = math.max(1, math.floor(tonumber(oldest[2]) + window_ms - now_ms))
  redis.call('PEXPIRE', KEYS[1], window_ms + 1000)
  return {0, used, retry_ms}
end
redis.call('ZADD', KEYS[1], now_ms, ARGV[3])
redis.call('PEXPIRE', KEYS[1], window_ms + 1000)
return {1, used + 1, 0}
`)

var inspectGroupMessageRateScript = redis.NewScript(`
local now_parts = redis.call('TIME')
local now_ms = tonumber(now_parts[1]) * 1000 + math.floor(tonumber(now_parts[2]) / 1000)
local window_ms = tonumber(ARGV[1])
local maximum = tonumber(ARGV[2])
local cutoff = now_ms - window_ms
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', cutoff)
local used = redis.call('ZCARD', KEYS[1])
local retry_ms = 0
if used >= maximum then
  local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
  retry_ms = math.max(1, math.floor(tonumber(oldest[2]) + window_ms - now_ms))
end
return {used, retry_ms}
`)

func groupMessageRateRedisKey(groupID, userID string, version int64) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", groupID, userID, version)))
	return "im:group-message-rate:" + hex.EncodeToString(digest[:])
}

func groupMessageRateStatus(limit, used int, retryMilliseconds int64) GroupMessageRateStatus {
	return GroupMessageRateStatus{
		LimitPerMinute:    limit,
		Used:              used,
		Remaining:         max(0, limit-used),
		RetryAfterSeconds: int(math.Ceil(float64(retryMilliseconds) / 1000)),
	}
}

func (p *WithRedis) ConsumeGroupMessageRate(ctx context.Context, groupID, userID string, version int64, limit int, window time.Duration) (GroupMessageRateStatus, error) {
	if limit <= 0 || window <= 0 {
		return GroupMessageRateStatus{}, nil
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return GroupMessageRateStatus{}, err
	}
	values, err := consumeGroupMessageRateScript.Run(ctx, p.redis, []string{groupMessageRateRedisKey(groupID, userID, version)}, window.Milliseconds(), limit, hex.EncodeToString(nonce[:])).Int64Slice()
	if err != nil {
		return GroupMessageRateStatus{}, err
	}
	if len(values) != 3 {
		return GroupMessageRateStatus{}, fmt.Errorf("unexpected group message rate response")
	}
	status := groupMessageRateStatus(limit, int(values[1]), values[2])
	if values[0] == 0 {
		return status, &GroupMessageRateLimitError{RetryAfterSeconds: status.RetryAfterSeconds}
	}
	return status, nil
}

func (p *WithRedis) GetGroupMessageRateStatus(ctx context.Context, groupID, userID string, version int64, limit int, window time.Duration) (GroupMessageRateStatus, error) {
	if limit <= 0 || window <= 0 {
		return GroupMessageRateStatus{}, nil
	}
	values, err := inspectGroupMessageRateScript.Run(ctx, p.redis, []string{groupMessageRateRedisKey(groupID, userID, version)}, window.Milliseconds(), limit).Int64Slice()
	if err != nil {
		return GroupMessageRateStatus{}, err
	}
	if len(values) != 2 {
		return GroupMessageRateStatus{}, fmt.Errorf("unexpected group message rate status response")
	}
	return groupMessageRateStatus(limit, int(values[0]), values[1]), nil
}
