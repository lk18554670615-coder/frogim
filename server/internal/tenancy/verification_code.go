package tenancy

// Fixed verification is an explicit operator configuration, never a fallback.
func ValidFixedVerificationCode(code string) bool {
	if code == "" {
		return true
	}
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
