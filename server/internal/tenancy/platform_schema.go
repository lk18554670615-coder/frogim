package tenancy

// PlatformSchemaVersion is independent of the enterprise schema. Deployment
// metadata and offline recovery tooling must agree with the directory server.
const PlatformSchemaVersion = 21

const PlatformMigrationLock int64 = 490739173

// Live directory connections exclude offline recovery mutations.
const PlatformRecoveryLock int64 = 490739174

// A recovery target stays quarantined until enterprise reconciliation has been
// explicitly completed. Merely restoring a valid backup cannot enable login.
const PlatformRecoverySchema = "frogim_recovery"
