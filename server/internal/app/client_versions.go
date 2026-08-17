package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
)

var clientVersionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}$`)

var supportedClientPlatforms = map[string]bool{
	"android": true,
	"ios":     true,
	"web":     true,
	"macos":   true,
}

type ClientVersionDecision struct {
	Platform          string    `json:"platform"`
	CurrentVersion    string    `json:"currentVersion"`
	MinimumVersion    string    `json:"minimumVersion"`
	LatestVersion     string    `json:"latestVersion"`
	UpdateAvailable   bool      `json:"updateAvailable"`
	ForceUpdate       bool      `json:"forceUpdate"`
	RolloutEligible   bool      `json:"rolloutEligible"`
	RolloutPercentage int       `json:"rolloutPercentage"`
	ReleaseNotes      string    `json:"releaseNotes"`
	DownloadURL       string    `json:"downloadUrl"`
	PublishedAt       time.Time `json:"publishedAt,omitempty"`
}

func normalizeClientPlatform(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func parseClientVersion(value string) ([4]uint64, bool) {
	var parsed [4]uint64
	value = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "v"))
	if !clientVersionPattern.MatchString(value) {
		return parsed, false
	}
	for index, part := range strings.Split(value, ".") {
		number, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return parsed, false
		}
		parsed[index] = number
	}
	return parsed, true
}

func compareClientVersions(left, right string) (int, bool) {
	l, validLeft := parseClientVersion(left)
	r, validRight := parseClientVersion(right)
	if !validLeft || !validRight {
		return 0, false
	}
	for index := range l {
		if l[index] < r[index] {
			return -1, true
		}
		if l[index] > r[index] {
			return 1, true
		}
	}
	return 0, true
}

func rolloutBucket(platform, installID string) int {
	digest := sha256.Sum256([]byte(platform + "\x00" + installID))
	return int(binary.BigEndian.Uint64(digest[:8]) % 100)
}

func validateClientVersionPolicy(policy store.ClientVersionPolicy) bool {
	policy.Platform = normalizeClientPlatform(policy.Platform)
	if !supportedClientPlatforms[policy.Platform] || len(policy.ReleaseNotes) > 4000 || policy.RolloutPercentage < 0 || policy.RolloutPercentage > 100 {
		return false
	}
	if _, valid := parseClientVersion(policy.MinimumVersion); !valid {
		return false
	}
	if _, valid := parseClientVersion(policy.LatestVersion); !valid {
		return false
	}
	if order, valid := compareClientVersions(policy.MinimumVersion, policy.LatestVersion); !valid || order > 0 {
		return false
	}
	if policy.DownloadURL != "" {
		parsed, err := url.ParseRequestURI(policy.DownloadURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return false
		}
	}
	return true
}

func (a *App) ListClientVersionPolicies(ctx context.Context) ([]store.ClientVersionPolicy, error) {
	policies, ok := a.persistence.(store.ClientVersionPolicyStore)
	if !ok {
		return []store.ClientVersionPolicy{}, nil
	}
	items, err := policies.ListClientVersionPolicies(ctx)
	if err != nil {
		return nil, mapStoreError(err)
	}
	return items, nil
}

func (a *App) ListClientVersionHistory(ctx context.Context, platform, cursor string, limit int) ([]store.ClientVersionReleaseRecord, int64, string, error) {
	platform = normalizeClientPlatform(platform)
	if !supportedClientPlatforms[platform] {
		return nil, 0, "", ErrInvalid
	}
	history, ok := a.persistence.(store.ClientVersionHistoryStore)
	if !ok {
		return []store.ClientVersionReleaseRecord{}, 0, "", nil
	}
	items, total, next, err := history.ListClientVersionHistory(ctx, platform, cursor, limit)
	if err != nil {
		return nil, 0, "", mapStoreError(err)
	}
	return items, total, next, nil
}

func (a *App) UpdateClientVersionPolicy(ctx context.Context, policy store.ClientVersionPolicy, actor, reason string, at time.Time) (*store.ClientVersionPolicy, error) {
	policy.Platform = normalizeClientPlatform(policy.Platform)
	policy.MinimumVersion = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(policy.MinimumVersion), "v"))
	policy.LatestVersion = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(policy.LatestVersion), "v"))
	policy.ReleaseNotes = strings.TrimSpace(policy.ReleaseNotes)
	policy.DownloadURL = strings.TrimSpace(policy.DownloadURL)
	reason = strings.TrimSpace(reason)
	if !validateClientVersionPolicy(policy) || reason == "" || len([]rune(reason)) > 500 {
		return nil, ErrInvalid
	}
	policies, ok := a.persistence.(store.ClientVersionPolicyStore)
	if !ok {
		return nil, ErrInvalid
	}
	updated, err := policies.UpsertClientVersionPolicy(ctx, policy, actor, reason, at)
	if err != nil {
		return nil, mapStoreError(err)
	}
	return updated, nil
}

func (a *App) EvaluateClientVersion(ctx context.Context, platform, currentVersion, installID string) (*ClientVersionDecision, error) {
	platform = normalizeClientPlatform(platform)
	currentVersion = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(currentVersion), "v"))
	installID = strings.TrimSpace(installID)
	if !supportedClientPlatforms[platform] || len(installID) < 8 || len(installID) > 128 {
		return nil, ErrInvalid
	}
	if _, valid := parseClientVersion(currentVersion); !valid {
		return nil, ErrInvalid
	}
	decision := &ClientVersionDecision{
		Platform: platform, CurrentVersion: currentVersion,
		MinimumVersion: currentVersion, LatestVersion: currentVersion,
		RolloutEligible: true, RolloutPercentage: 100,
	}
	policies, ok := a.persistence.(store.ClientVersionPolicyStore)
	if !ok {
		return decision, nil
	}
	policy, err := policies.GetClientVersionPolicy(ctx, platform)
	if err == store.ErrNotFound {
		return decision, nil
	}
	if err != nil {
		return nil, mapStoreError(err)
	}
	belowMinimum, valid := compareClientVersions(currentVersion, policy.MinimumVersion)
	if !valid {
		return nil, ErrInvalid
	}
	belowLatest, valid := compareClientVersions(currentVersion, policy.LatestVersion)
	if !valid {
		return nil, ErrInvalid
	}
	eligible := rolloutBucket(platform, installID) < policy.RolloutPercentage
	forcedByMinimum := belowMinimum < 0
	updateAvailable := forcedByMinimum || (belowLatest < 0 && eligible)
	decision.MinimumVersion = policy.MinimumVersion
	decision.LatestVersion = policy.LatestVersion
	decision.UpdateAvailable = updateAvailable
	decision.ForceUpdate = updateAvailable && (forcedByMinimum || policy.ForceUpdate)
	decision.RolloutEligible = eligible || forcedByMinimum
	decision.RolloutPercentage = policy.RolloutPercentage
	decision.ReleaseNotes = policy.ReleaseNotes
	decision.DownloadURL = policy.DownloadURL
	decision.PublishedAt = policy.UpdatedAt
	return decision, nil
}
