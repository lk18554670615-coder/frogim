package app

import (
	"context"
	"strings"
	"time"

	"github.com/linli/im/server/internal/clientversion"
	"github.com/linli/im/server/internal/store"
)

type ClientVersionDecision = clientversion.Decision

func compareClientVersions(a, b string) (int, bool)                { return clientversion.Compare(a, b) }
func validateClientVersionPolicy(p store.ClientVersionPolicy) bool { return clientversion.Valid(p) }

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
	platform = clientversion.Platform(platform)
	if !clientversion.Supported(platform) {
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
	policy.Platform = clientversion.Platform(policy.Platform)
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
	decision, err := clientversion.Evaluate(platform, currentVersion, installID, nil)
	if err != nil {
		return nil, ErrInvalid
	}
	policies, ok := a.persistence.(store.ClientVersionPolicyStore)
	if !ok {
		return decision, nil
	}
	policy, err := policies.GetClientVersionPolicy(ctx, decision.Platform)
	if err == store.ErrNotFound {
		return decision, nil
	}
	if err != nil {
		return nil, mapStoreError(err)
	}
	decision, err = clientversion.Evaluate(platform, currentVersion, installID, policy)
	if err != nil {
		return nil, ErrInvalid
	}
	return decision, nil
}
