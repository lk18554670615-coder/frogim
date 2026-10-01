package tenancy

import (
	"regexp"
	"strconv"
	"strings"
)

var versionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}$`)

func versionCompare(a, b string) (int, bool) {
	if !versionPattern.MatchString(a) || !versionPattern.MatchString(b) {
		return 0, false
	}
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 4; i++ {
		var x, y uint64
		var e error
		if i < len(aa) {
			x, e = strconv.ParseUint(aa[i], 10, 31)
			if e != nil {
				return 0, false
			}
		}
		if i < len(bb) {
			y, e = strconv.ParseUint(bb[i], 10, 31)
			if e != nil {
				return 0, false
			}
		}
		if x < y {
			return -1, true
		}
		if x > y {
			return 1, true
		}
	}
	return 0, true
}
func versionDecision(platform, current string, policy map[string]any) map[string]any {
	latest, _ := policy["latestVersion"].(string)
	minimum, _ := policy["minimumVersion"].(string)
	if latest == "" {
		latest = current
	}
	if minimum == "" {
		minimum = current
	}
	order, _ := versionCompare(current, latest)
	older, _ := versionCompare(current, minimum)
	force, _ := policy["forceUpdate"].(bool)
	return map[string]any{"platform": platform, "currentVersion": current, "minimumVersion": minimum, "latestVersion": latest, "updateAvailable": order < 0, "forceUpdate": older < 0 || (force && order < 0), "rolloutEligible": true, "rolloutPercentage": 100, "downloadUrl": policy["downloadUrl"], "releaseNotes": policy["releaseNotes"]}
}
