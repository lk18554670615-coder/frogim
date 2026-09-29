package tenancy

import "testing"

func TestAddressTrustBoundary(t *testing.T) {
	for _, raw := range []string{"https://enterprise.example", "https://enterprise.example:443/"} {
		if ValidateBaseURL(raw, false) != nil {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"http://enterprise.example", "https://u:p@enterprise.example", "https://enterprise.example/api", "https://enterprise.example?tenant=b", "https://enterprise.example#fragment", "//enterprise.example", " https://enterprise.example", "javascript:alert(1)", "https://enterprise.example/%2f"} {
		if ValidateBaseURL(raw, true) == nil {
			t.Fatalf("accepted untrusted address %q", raw)
		}
	}
	if ValidateBaseURL("http://127.0.0.1:8080", true) != nil || ValidateBaseURL("http://127.0.0.1:8080", false) == nil {
		t.Fatal("loopback exception")
	}
}

func TestIdentityAndSecret(t *testing.T) {
	i := Identity{AccountID: "a", TenantID: "t", LocalUserID: "u", AssignmentVersion: 1}
	if i.Validate() != nil {
		t.Fatal(i)
	}
	i.AssignmentVersion = 0
	if i.Validate() == nil {
		t.Fatal("zero generation")
	}
	a, err := Secret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Secret()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || len(a) != 43 || len(Hash(a)) != 32 {
		t.Fatal("opaque secret entropy/encoding")
	}
}
