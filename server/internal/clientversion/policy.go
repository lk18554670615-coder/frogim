// Package clientversion contains pure rollout rules shared by the platform and
// the legacy single-enterprise service, without either database dependency.
package clientversion

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid client version policy or query")

type Policy struct {
	Platform          string    `json:"platform"`
	MinimumVersion    string    `json:"minimumVersion"`
	LatestVersion     string    `json:"latestVersion"`
	ForceUpdate       bool      `json:"forceUpdate"`
	RolloutPercentage int       `json:"rolloutPercentage"`
	ReleaseNotes      string    `json:"releaseNotes"`
	DownloadURL       string    `json:"downloadUrl"`
	UpdatedBy         string    `json:"updatedBy"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

var clientVersionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}$`)

var supportedClientPlatforms = map[string]bool{
	"android": true,
	"ios":     true,
	"web":     true,
	"macos":   true,
}

type Decision struct {
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

func Platform(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func Parse(value string) ([4]uint64, bool) {
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

func Compare(left, right string) (int, bool) {
	l, validLeft := Parse(left)
	r, validRight := Parse(right)
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

func Bucket(platform, installID string) int {
	digest := sha256.Sum256([]byte(platform + "\x00" + installID))
	return int(binary.BigEndian.Uint64(digest[:8]) % 100)
}

func Valid(policy Policy) bool {
	policy.Platform = Platform(policy.Platform)
	if !supportedClientPlatforms[policy.Platform] || len(policy.ReleaseNotes) > 4000 || policy.RolloutPercentage < 0 || policy.RolloutPercentage > 100 {
		return false
	}
	if _, valid := Parse(policy.MinimumVersion); !valid {
		return false
	}
	if _, valid := Parse(policy.LatestVersion); !valid {
		return false
	}
	if order, valid := Compare(policy.MinimumVersion, policy.LatestVersion); !valid || order > 0 {
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

func Supported(platform string) bool { return supportedClientPlatforms[platform] }
func Normalize(p Policy) Policy {
	p.Platform = Platform(p.Platform)
	p.MinimumVersion = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(p.MinimumVersion), "v"))
	p.LatestVersion = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(p.LatestVersion), "v"))
	p.ReleaseNotes = strings.TrimSpace(p.ReleaseNotes)
	p.DownloadURL = strings.TrimSpace(p.DownloadURL)
	return p
}
func Evaluate(platform, currentVersion, installID string, policy *Policy) (*Decision, error) {
	platform = Platform(platform)
	currentVersion = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(currentVersion), "v"))
	installID = strings.TrimSpace(installID)
	if !supportedClientPlatforms[platform] || len(installID) < 8 || len(installID) > 128 {
		return nil, ErrInvalid
	}
	if _, valid := Parse(currentVersion); !valid {
		return nil, ErrInvalid
	}
	decision := &Decision{
		Platform: platform, CurrentVersion: currentVersion,
		MinimumVersion: currentVersion, LatestVersion: currentVersion,
		RolloutEligible: true, RolloutPercentage: 100,
	}
	if policy == nil {
		return decision, nil
	}
	if !Valid(*policy) || policy.Platform != platform {
		return nil, ErrInvalid
	}
	belowMinimum, valid := Compare(currentVersion, policy.MinimumVersion)
	if !valid {
		return nil, ErrInvalid
	}
	belowLatest, valid := Compare(currentVersion, policy.LatestVersion)
	if !valid {
		return nil, ErrInvalid
	}
	eligible := Bucket(platform, installID) < policy.RolloutPercentage
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
