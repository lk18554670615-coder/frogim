import Foundation

struct Failed: Error { let message: String }
@main struct TenantCallPolicyTests {
  static func require(_ value: @autoclosure () -> Bool, _ message: String) throws {
    if !value() { throw Failed(message: message) }
  }
  static func main() throws {
    typealias P = LinliTenantCallPolicy
    let now = P.expiry("2026-09-29T00:00:00Z")!
    let scope: [String: Any] = ["tenantId": "tenant-a", "localUserId": "user-a", "assignmentVersion": 1,
      "authVersion": 2, "realmVersion": 3, "expiresAt": "2099-01-01T00:00:00Z",
      "callSessionId": String(repeating: "a", count: 43)]
    var binding = scope
    binding.removeValue(forKey: "callSessionId")
    binding.removeValue(forKey: "expiresAt")
    binding["provider"] = "getui_voip"
    binding["pushBindingId"] = "binding-1"
    binding["pushBindingRevision"] = 1
    binding["leaseExpiresAt"] = "2099-01-01T00:00:00Z"
    var payload = binding; payload["expiresAt"] = "2026-09-29T00:00:30Z"
    let state: [String: Any] = ["scope": scope, "bindings": [binding]]
    let parsed = P.incoming(state, payload: payload, now: now)!
    let wrapped = ["payload": String(data: try JSONSerialization.data(withJSONObject: payload), encoding: .utf8)!]
    try require(P.incoming(state, payload: P.pushBody(wrapped), now: now) != nil, "Getui payload envelope")
    var obsolete = binding; obsolete["provider"] = "apns_voip"
    try require(P.incoming(["scope": scope, "bindings": [obsolete]], payload: payload, now: now) == nil, "old direct binding rejected")
    try require(parsed.seed("call-1") == "tenant-call-v1|tenant-a|user-a|1|2|3|\(String(repeating: "a", count: 43))|call-1", "shared seed")
    print("PASS: Swift identity and seed match Dart/Android")
    for key in P.identityKeys + ["pushBindingId", "pushBindingRevision"] {
      var missing = payload; missing.removeValue(forKey: key)
      try require(P.incoming(state, payload: missing, now: now) == nil, "missing \(key)")
      var changed = payload
      changed[key] = P.version(payload[key]).map { $0 + 1 } ?? "different" as Any
      try require(P.incoming(state, payload: changed, now: now) == nil, "different \(key)")
    }
    print("PASS: every identity and binding field is required")
    for key in ["expiresAt", "leaseExpiresAt", "scope"] {
      var s = state; var p = payload
      if key == "expiresAt" { p[key] = "2026-09-29T00:00:00Z" }
      if key == "leaseExpiresAt" { var b = binding; b[key] = "2026-09-29T00:00:00Z"; s["bindings"] = [b] }
      if key == "scope" { var old = scope; old["expiresAt"] = "2026-09-29T00:00:00Z"; s[key] = old }
      try require(P.incoming(s, payload: p, now: now) == nil, "expired \(key)")
    }
    print("PASS: local session, payload and binding expiry boundaries")
    for value: Any in [0, -1, 1.5, true, "1", 9007199254740992 as Int64] {
      try require(P.version(value) == nil, "unsafe version")
    }
    for bad in ["2099-02-30T00:00:00Z", "2099-01-01", "2099-01-01T00:00:00",
      "2099-01-01T25:00:00Z", "2099-01-01T00:00:00+25:00"] {
      try require(P.expiry(bad) == nil, "invalid timestamp \(bad)")
    }
    try require(P.expiry("2026-09-29T08:00:00+08:00") == now, "offset")
    print("PASS: malformed fields and timezone validation")
    var relogin = scope; relogin["callSessionId"] = String(repeating: "b", count: 43)
    try require(!P.action(state, extra: ["tenantScope": relogin], now: now), "same-account old login")
    try require(P.action(state, extra: ["tenantScope": scope], now: now), "valid action")
    print("PASS: login generation gates native actions")
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("tenant-call-tests-" + UUID().uuidString)
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    let file = root.appendingPathComponent("identity.json")
    let native = LinliTenantCallState(managed: true, file: file)
    let first = native.beginEngine()
    var input: [String: Any] = ["control": first, "sequence": 1, "scope": scope, "bindings": [binding]]
    try require(native.update(input), "first identity persisted")
    try require(!native.update(input), "duplicate sequence rejected")
    let restored = LinliTenantCallState(managed: true, file: file)
    try require(restored.allows(["tenantScope": scope]), "cold restore uses persisted exact scope")
    print("PASS: acknowledged atomic identity and cold restore")
    let second = native.beginEngine()
    input["sequence"] = 999
    try require(!native.update(input), "old engine cannot steal control")
    input["control"] = second; input["sequence"] = 1; input["scope"] = relogin
    try require(native.update(input), "new engine identity")
    try require(!native.allows(["tenantScope": scope]), "old action after re-login")
    print("PASS: engine control and monotonic writes")
    input["sequence"] = 2; input["scope"] = NSNull(); input["bindings"] = [[String: Any]]()
    try require(native.update(input), "logout persisted")
    try require(!native.allows(["tenantScope": relogin]), "logout immediately hidden")
    try require(!LinliTenantCallState(managed: true, file: file).allows(["tenantScope": relogin]), "logout after restart")
    print("PASS: logout survives process restart")
    var withSecrets = binding; withSecrets["token"] = "synthetic-do-not-persist"
    input["sequence"] = 3; input["scope"] = scope; input["bindings"] = [withSecrets]
    try require(native.update(input), "binding sanitized")
    let disk = try String(contentsOf: file, encoding: .utf8)
    try require(!disk.contains("synthetic-do-not-persist"), "secrets stripped")
    print("PASS: native state is credential-free")
    native.invalidateVoipBinding()
    try require(P.incoming(native.value, payload: payload, now: now) == nil, "token invalidation removes binding immediately")
    try require(P.incoming(LinliTenantCallState(managed: true, file: file).value, payload: payload, now: now) == nil, "token invalidation survives restart")
    let impossible = LinliTenantCallState(managed: true, file: file.appendingPathComponent("child.json"))
    input["control"] = impossible.beginEngine(); input["sequence"] = 1
    try require(!impossible.update(input), "disk failure returns rejection")
    try require(!impossible.allows(["tenantScope": scope]), "disk failure cannot grant access")
    print("PASS: storage failure fails closed")
    let legacy = LinliTenantCallState(managed: false, file: root.appendingPathComponent("legacy.json"))
    try require(legacy.allows(["serverCallId": "legacy"]), "standalone compatibility")
    try require(!legacy.allows(["tenantScope": scope]), "no managed-to-legacy fallback")
    print("PASS: explicit legacy mode is isolated")
  }
}
