import Foundation
import CoreFoundation

// Foundation-only policy. No API/IM/provider credentials belong in this state.
enum LinliTenantCallPolicy {
  static func pushBody(_ body: [String: Any]) -> [String: Any] {
    guard let raw = body["payload"] as? String, let data = raw.data(using: .utf8), data.count <= 8192,
          let nested = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return body }
    return nested
  }
  static let identityKeys = ["tenantId", "localUserId", "assignmentVersion", "authVersion", "realmVersion"]
  static func matches(_ text: String, _ pattern: String) -> Bool {
    text.range(of: pattern, options: .regularExpression) == text.startIndex..<text.endIndex
  }
  static func version(_ value: Any?) -> Int64? {
    guard let n = value as? NSNumber, CFGetTypeID(n) != CFBooleanGetTypeID(),
          n.doubleValue.isFinite, n.doubleValue >= 1, n.doubleValue <= 9007199254740991,
          Double(n.int64Value) == n.doubleValue else { return nil }
    return n.int64Value
  }
  static func expiry(_ value: Any?) -> Date? {
    guard let text = value as? String,
          matches(text, #"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})"#)
    else { return nil }
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    let base = String(text.prefix(19))
    let tail = String(text.dropFirst(19))
    let zone = text.hasSuffix("Z") ? "Z" : String(text.suffix(6))
    let fraction = tail.hasPrefix(".") ? String(tail.dropFirst().prefix(while: { $0.isNumber })) : ""
    let normalized = base + "." + String((fraction + "000").prefix(3)) + zone
    guard let result = formatter.date(from: normalized) else { return nil }
    // ISO8601DateFormatter can normalize an invalid day on some platforms.
    let strict = DateFormatter()
    strict.locale = Locale(identifier: "en_US_POSIX")
    strict.calendar = Calendar(identifier: .gregorian)
    strict.isLenient = false
    strict.dateFormat = "yyyy-MM-dd'T'HH:mm:ss.SSSXXXXX"
    guard strict.date(from: normalized) != nil else { return nil }
    return result
  }
  struct Scope {
    let fields: [String: Any]
    let expires: Date
    func sameIdentity(_ other: Scope) -> Bool {
      LinliTenantCallPolicy.identityKeys.allSatisfy {
        String(describing: fields[$0]!) == String(describing: other.fields[$0]!)
      }
    }
    func sameSession(_ other: Scope) -> Bool {
      sameIdentity(other) && fields["callSessionId"] as? String != nil &&
        fields["callSessionId"] as? String == other.fields["callSessionId"] as? String
    }
    func seed(_ callID: String) -> String {
      "tenant-call-v1|" + (LinliTenantCallPolicy.identityKeys + ["callSessionId"])
        .map { String(describing: fields[$0]!) }.joined(separator: "|") + "|" + callID
    }
  }
  static func scope(_ input: Any?, requireSession: Bool = true) -> Scope? {
    guard let input = input as? [String: Any],
          let tenant = input["tenantId"] as? String, let user = input["localUserId"] as? String,
          matches(tenant, #"[A-Za-z0-9][A-Za-z0-9_-]{0,79}"#),
          matches(user, #"[A-Za-z0-9][A-Za-z0-9_-]{0,79}"#),
          let date = expiry(input["expiresAt"]) else { return nil }
    var data: [String: Any] = ["tenantId": tenant, "localUserId": user, "expiresAt": input["expiresAt"]!]
    for key in identityKeys.dropFirst(2) {
      guard let number = version(input[key]) else { return nil }
      data[key] = number
    }
    if let generation = input["callSessionId"] {
      guard let text = generation as? String, matches(text, #"[A-Za-z0-9_-]{43}"#) else { return nil }
      data["callSessionId"] = text
    } else if requireSession { return nil }
    return Scope(fields: data, expires: date)
  }
  static func incoming(_ state: [String: Any], payload: [String: Any], now: Date = Date()) -> Scope? {
    guard let current = scope(state["scope"]), current.expires > now,
          let pushed = scope(payload, requireSession: false), pushed.expires > now,
          current.sameIdentity(pushed), let id = payload["pushBindingId"] as? String,
          matches(id, #"[A-Za-z0-9][A-Za-z0-9_-]{0,79}"#),
          let revision = version(payload["pushBindingRevision"]),
          let bindings = state["bindings"] as? [[String: Any]] else { return nil }
    let valid = bindings.contains { binding in
      var value = binding
      value["expiresAt"] = binding["leaseExpiresAt"]
      return binding["provider"] as? String == "getui_voip" && binding["pushBindingId"] as? String == id &&
        version(binding["pushBindingRevision"]) == revision &&
        (expiry(binding["leaseExpiresAt"]) ?? .distantPast) > now &&
        scope(value, requireSession: false)?.sameIdentity(current) == true
    }
    return valid ? current : nil
  }
  static func action(_ state: [String: Any], extra: [String: Any], now: Date = Date()) -> Bool {
    guard let current = scope(state["scope"]), let action = scope(extra["tenantScope"]) else { return false }
    return current.expires > now && action.expires > now && current.sameSession(action)
  }
}

// All accesses are on the native main queue. Atomic writes are acknowledged
// before Flutter exposes a session; a write failure revokes in-memory access.
final class LinliTenantCallState {
  let managed: Bool
  private let file: URL
  private(set) var value: [String: Any] = [:]
  private var control = UUID().uuidString
  private var sequence: Int64 = 0
  init(managed: Bool, file: URL) {
    self.managed = managed
    self.file = file
    if let data = try? Foundation.Data(contentsOf: file), data.count <= 32768,
       let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] { value = object }
  }
  func beginEngine() -> String {
    sequence = 0
    control = UUID().uuidString
    return control
  }
  func invalidateVoipBinding() {
    guard managed else { return }
    value["bindings"] = (value["bindings"] as? [[String: Any]] ?? []).filter { $0["provider"] as? String != "getui_voip" }
    do { try Self.write(value, to: file) } catch { value = [:]; try? FileManager.default.removeItem(at: file) }
  }
  func update(_ input: [String: Any]) -> Bool {
    guard managed, input["control"] as? String == control,
          let next = LinliTenantCallPolicy.version(input["sequence"]), next > sequence,
          let bindings = input["bindings"] as? [[String: Any]], bindings.count <= 2 else { return false }
    let rawScope = input["scope"] is NSNull ? nil : input["scope"]
    let parsed = LinliTenantCallPolicy.scope(rawScope)
    if rawScope != nil && parsed == nil { return false }
    if parsed == nil && !bindings.isEmpty { return false }
    var safeBindings = [[String: Any]]()
    var providers = Set<String>()
    for binding in bindings {
      guard let provider = binding["provider"] as? String, ["getui", "getui_voip"].contains(provider),
            providers.insert(provider).inserted, let parsed = parsed else { return false }
      var candidate = binding
      candidate["expiresAt"] = binding["leaseExpiresAt"]
      // Same identity validation; only Getui VoIP is a managed PushKit source.
      var test = binding
      test["provider"] = "getui_voip"
      guard LinliTenantCallPolicy.incoming(["scope": parsed.fields, "bindings": [test]], payload: candidate) != nil
      else { return false }
      let keys = LinliTenantCallPolicy.identityKeys + ["provider", "deviceId", "pushBindingId", "pushBindingRevision", "leaseExpiresAt"]
      safeBindings.append(Dictionary(uniqueKeysWithValues: keys.compactMap { key in binding[key].map { (key, $0) } }))
    }
    let replacement: [String: Any] = ["scope": parsed.map { $0.fields as Any } ?? NSNull(), "bindings": safeBindings]
    do {
      try Self.write(replacement, to: file)
      value = replacement
      sequence = next
      return true
    } catch {
      value = [:]
      return false
    }
  }
  func allows(_ extra: [String: Any]) -> Bool {
    managed ? LinliTenantCallPolicy.action(value, extra: extra) : extra["tenantScope"] == nil
  }
  static func write(_ value: Any, to file: URL) throws {
    try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
    let data = try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
    #if os(iOS)
    try data.write(to: file, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    var target = file
    var resources = URLResourceValues()
    resources.isExcludedFromBackup = true
    try target.setResourceValues(resources)
    #else
    try data.write(to: file, options: .atomic)
    #endif
  }
}
