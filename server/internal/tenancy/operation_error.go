package tenancy

// Only these codes may cross the mTLS boundary. Do not attach arbitrary remote
// messages, request bodies, database errors, passwords or tokens to this error.
type OperationRejected struct{ Code string }

func (e *OperationRejected) Error() string { return e.Code }

func RepairableCode(code string) bool {
	switch code {
	case "GROUP_OWNERSHIP_TRANSFER_REQUIRED", "TENANT_REGISTRATION_DISABLED", "TENANT_PASSWORD_POLICY_REJECTED", "INVITE_REQUIRED", "INVITE_INVALID", "INVITE_DISABLED":
		return true
	}
	return false
}
