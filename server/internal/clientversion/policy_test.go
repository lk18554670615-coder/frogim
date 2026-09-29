package clientversion

import "testing"

func TestEvaluateSharedRollout(t *testing.T) {
	p := Policy{Platform: "android", MinimumVersion: "1.2.0", LatestVersion: "2.0.0", DownloadURL: "https://downloads.example/app.apk"}
	for _, c := range []struct {
		version                    string
		percentage                 int
		force, available, required bool
	}{
		{"1.0.0", 0, false, true, true}, {"1.2.0", 0, false, false, false}, {"1.2.0", 100, false, true, false},
		{"1.2.0", 100, true, true, true}, {"2.0.0", 100, true, false, false}, {"3.0.0", 100, true, false, false},
	} {
		p.RolloutPercentage, p.ForceUpdate = c.percentage, c.force
		d, e := Evaluate("android", c.version, "stable-install-123", &p)
		if e != nil || d.UpdateAvailable != c.available || d.ForceUpdate != c.required {
			t.Fatal(c, d, e)
		}
	}
	p.RolloutPercentage = 50
	a, _ := Evaluate("android", "1.3.0", "stable-install-123", &p)
	b, _ := Evaluate("android", "1.3.0", "stable-install-123", &p)
	if *a != *b {
		t.Fatal("unstable cohort")
	}
	for _, v := range []string{"", "1-beta", "2147483648", "1.2.3.4.5"} {
		if _, e := Evaluate("android", v, "stable-install-123", nil); e == nil {
			t.Fatal("invalid version", v)
		}
	}
	if _, e := Evaluate("unknown", "1.0", "stable-install-123", nil); e == nil {
		t.Fatal("unknown platform")
	}
	if _, e := Evaluate("android", "1.0", "short", nil); e == nil {
		t.Fatal("short installation id")
	}
	if _, e := Evaluate("ios", "1.0", "stable-install-123", &p); e == nil {
		t.Fatal("foreign policy")
	}
}
