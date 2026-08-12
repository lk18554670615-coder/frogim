package app

import (
	"context"
	"testing"
	"time"

	"github.com/linli/im/server/internal/store"
)

type clientVersionTestStore struct {
	store.Memory
	policies map[string]store.ClientVersionPolicy
}

func (s *clientVersionTestStore) ListClientVersionPolicies(context.Context) ([]store.ClientVersionPolicy, error) {
	items := make([]store.ClientVersionPolicy, 0, len(s.policies))
	for _, policy := range s.policies {
		items = append(items, policy)
	}
	return items, nil
}

func (s *clientVersionTestStore) GetClientVersionPolicy(_ context.Context, platform string) (*store.ClientVersionPolicy, error) {
	policy, ok := s.policies[platform]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &policy, nil
}

func (s *clientVersionTestStore) UpsertClientVersionPolicy(_ context.Context, policy store.ClientVersionPolicy, actor, _ string, at time.Time) (*store.ClientVersionPolicy, error) {
	policy.UpdatedBy = actor
	policy.UpdatedAt = at
	s.policies[policy.Platform] = policy
	return &policy, nil
}

func TestClientVersionComparisonAndValidation(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
		valid       bool
	}{
		{"1.2.3", "1.2.3", 0, true},
		{"1.2", "1.2.0", 0, true},
		{"1.10.0", "1.9.9", 1, true},
		{"2.0.0", "10.0.0", -1, true},
		{"1.2-beta", "1.2.0", 0, false},
	}
	for _, test := range cases {
		got, valid := compareClientVersions(test.left, test.right)
		if valid != test.valid || got != test.want {
			t.Errorf("compare %q %q = (%d,%v), want (%d,%v)", test.left, test.right, got, valid, test.want, test.valid)
		}
	}
	if validateClientVersionPolicy(store.ClientVersionPolicy{Platform: "android", MinimumVersion: "2.0.0", LatestVersion: "1.0.0", RolloutPercentage: 100}) {
		t.Fatal("minimum version newer than latest was accepted")
	}
	if validateClientVersionPolicy(store.ClientVersionPolicy{Platform: "android", MinimumVersion: "1.0.0", LatestVersion: "2.0.0", RolloutPercentage: 100, DownloadURL: "http://downloads.example.com/app.apk"}) {
		t.Fatal("insecure download URL was accepted")
	}
}

func TestClientVersionRolloutIsStableAndMinimumAlwaysWins(t *testing.T) {
	ctx := context.Background()
	policy := store.ClientVersionPolicy{
		Platform: "android", MinimumVersion: "1.5.0", LatestVersion: "2.0.0",
		ForceUpdate: false, RolloutPercentage: 1, ReleaseNotes: "新版", DownloadURL: "https://downloads.example.com/app.apk",
		UpdatedAt: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
	}
	repository := &clientVersionTestStore{policies: map[string]store.ClientVersionPolicy{"android": policy}}
	a, err := New(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.EvaluateClientVersion(ctx, "android", "1.0.0", "install-00000001")
	if err != nil {
		t.Fatal(err)
	}
	if !first.UpdateAvailable || !first.ForceUpdate || !first.RolloutEligible {
		t.Fatalf("minimum version must force every cohort: %+v", first)
	}
	optionalA, err := a.EvaluateClientVersion(ctx, "android", "1.9.0", "install-00000001")
	if err != nil {
		t.Fatal(err)
	}
	optionalB, err := a.EvaluateClientVersion(ctx, "android", "1.9.0", "install-00000001")
	if err != nil {
		t.Fatal(err)
	}
	if *optionalA != *optionalB {
		t.Fatalf("same install changed rollout decision: first=%+v second=%+v", optionalA, optionalB)
	}
	if optionalA.UpdateAvailable != optionalA.RolloutEligible {
		t.Fatalf("optional update did not follow cohort: %+v", optionalA)
	}
}

func TestUpdateClientVersionPolicyRequiresReasonAndNormalizesVersions(t *testing.T) {
	ctx := context.Background()
	repository := &clientVersionTestStore{policies: map[string]store.ClientVersionPolicy{}}
	a, err := New(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	input := store.ClientVersionPolicy{
		Platform: "MACOS", MinimumVersion: "v1.0", LatestVersion: "V1.2.0",
		RolloutPercentage: 25, DownloadURL: "https://downloads.example.com/macos.dmg",
	}
	if _, err = a.UpdateClientVersionPolicy(ctx, input, "admin", "", time.Now()); err != ErrInvalid {
		t.Fatalf("missing reason err=%v", err)
	}
	updated, err := a.UpdateClientVersionPolicy(ctx, input, "admin", "发布 1.2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Platform != "macos" || updated.MinimumVersion != "1.0" || updated.LatestVersion != "1.2.0" {
		t.Fatalf("policy was not normalized: %+v", updated)
	}
}
