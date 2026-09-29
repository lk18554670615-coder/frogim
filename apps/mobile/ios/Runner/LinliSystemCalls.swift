import Foundation
import Flutter
import CallKit
import AVFAudio
import CryptoKit
import WebRTC

// One application-owned provider, initialized before PushKit. All operations
// stay on the main queue, including identity changes and report completions.
final class LinliSystemCalls: NSObject, CXProviderDelegate {
  private final class Entry {
    let id: UUID
    let serverID: String
    var extra: [String: Any]
    var displayName = "青蛙呱呱联系人"
    var handle = "青蛙呱呱"
    let video: Bool
    let outgoing: Bool
    let expires: Date
    var accepted = false
    var connected = false
    var muted = false
    var reporting = false
    var deadline: DispatchWorkItem?
    var answer: CXAnswerCallAction?
    init(id: UUID, serverID: String, extra: [String: Any], video: Bool, outgoing: Bool, expires: Date) {
      self.id = id; self.serverID = serverID; self.extra = extra
      self.video = video; self.outgoing = outgoing; self.expires = expires
    }
    func action(_ type: String) -> [String: Any] {
      var result: [String: Any] = ["type": type, "serverCallId": serverID, "systemCallId": id.uuidString.lowercased()]
      result["tenantScope"] = extra["tenantScope"]
      return result
    }
    var saved: [String: Any] {
      ["id": id.uuidString, "serverCallId": serverID, "extra": extra, "video": video,
       "outgoing": outgoing, "accepted": accepted,
       "expiresAt": ISO8601DateFormatter().string(from: expires)]
    }
  }
  private let provider: CXProvider
  private let controller = CXCallController()
  private let observer = CXCallObserver()
  private let state: LinliTenantCallState
  private let ledger: URL
  private var channel: FlutterMethodChannel?
  private var entries: [UUID: Entry] = [:]
  private var pending = [[String: Any]]()
  var voipToken = ""

  func invalidateVoipBinding() { state.invalidateVoipBinding() }

  override init() {
    let modeURL = Bundle.main.url(forResource: "linli_call_mode", withExtension: "json")
    let mode = modeURL.flatMap { try? Foundation.Data(contentsOf: $0) }
      .flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }
    // An absent/corrupt build marker cannot switch a managed binary to legacy.
    let managed = mode?["schema"] as? Int == 1 ? (mode?["managed"] as? Bool ?? true) : true
    let root = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
      .appendingPathComponent("tenant-calls", isDirectory: true)
    state = LinliTenantCallState(managed: managed, file: root.appendingPathComponent("identity.json"))
    ledger = root.appendingPathComponent("calls.json")
    let config = CXProviderConfiguration(localizedName: "青蛙呱呱")
    config.supportsVideo = true
    config.supportedHandleTypes = [.generic]
    config.maximumCallGroups = 1
    config.maximumCallsPerCallGroup = 1
    config.includesCallsInRecents = false
    provider = CXProvider(configuration: config)
    super.init()
    provider.setDelegate(self, queue: .main)
    restoreLedger()
  }

  func attach(_ messenger: FlutterBinaryMessenger) {
    channel?.setMethodCallHandler(nil)
    let control = state.beginEngine()
    let bridge = FlutterMethodChannel(name: "top.hongjinghuanqiu.app/system_calls", binaryMessenger: messenger)
    channel = bridge
    bridge.setMethodCallHandler { [weak self] call, result in
      guard let self = self else { result(FlutterError(code: "CALLS_UNAVAILABLE", message: nil, details: nil)); return }
      let args = call.arguments as? [String: Any] ?? [:]
      switch call.method {
      case "beginTenantCallControl": result(control)
      case "setTenantCallState":
        let registration = GetuiflutPlugin.voipRegistration()
        let bindings = args["bindings"] as? [[String: Any]] ?? []
        if bindings.contains(where: { $0["provider"] as? String == "getui_voip" &&
            (registration["ready"] as? Bool != true || $0["deviceId"] as? String != registration["deviceId"] as? String) }) {
          result(false)
          return
        }
        let accepted = self.state.update(args)
        self.endInvalidCalls()
        result(accepted)
      case "voipToken": result(self.voipToken)
      case "drainLaunchActions":
        let active = Set(self.observer.calls.map { $0.uuid })
        for entry in Array(self.entries.values) where !entry.reporting && !active.contains(entry.id) {
          self.finish(entry.id, reason: .failed)
        }
        self.endInvalidCalls()
        let actions = self.pending + self.entries.values.filter { !$0.reporting }
          .map { $0.action($0.accepted ? "accept" : "restore") }
        self.pending.removeAll()
        result(actions)
      case "showIncoming", "showOutgoing":
        guard let entry = self.entry(args, outgoing: call.method == "showOutgoing") else {
          result(false); return
        }
        if self.entries[entry.id] != nil { result(true); return }
        guard self.entries.isEmpty else { result(false); return }
        self.entries[entry.id] = entry
        guard self.saveLedger() else { self.entries.removeValue(forKey: entry.id); result(false); return }
        if entry.outgoing {
          entry.reporting = true
          let action = CXStartCallAction(call: entry.id, handle: CXHandle(type: .generic, value: "青蛙呱呱"))
          action.isVideo = entry.video
          self.controller.request(CXTransaction(action: action)) { error in
            DispatchQueue.main.async {
              entry.reporting = false
              guard self.entries[entry.id] === entry else { result(false); return }
              if error != nil { self.finish(entry.id, reason: .failed) }
              result(error == nil && self.state.allows(entry.extra))
            }
          }
        } else {
          self.report(entry) { result($0) }
        }
      case "endCall":
        if let id = (args["id"] as? String).flatMap(UUID.init(uuidString:)) {
          self.finish(id, reason: .remoteEnded)
        }
        result(true)
      case "callConnected":
        guard let entry = self.current(args) else { result(false); return }
        entry.deadline?.cancel()
        entry.connected = true
        if entry.outgoing {
          self.provider.reportOutgoingCall(with: entry.id, connectedAt: Date())
        } else if let answer = entry.answer {
          answer.fulfill()
          entry.answer = nil
        } else if !entry.accepted {
          self.controller.request(CXTransaction(action: CXAnswerCallAction(call: entry.id))) { error in
            if error != nil { DispatchQueue.main.async { self.finish(entry.id, reason: .failed, event: "end") } }
          }
        }
        result(true)
      case "muteCall":
        guard let entry = self.current(args), let muted = args["muted"] as? Bool else { result(false); return }
        if entry.muted != muted {
          self.controller.request(CXTransaction(action: CXSetMutedCallAction(call: entry.id, muted: muted))) { error in
            DispatchQueue.main.async { result(error == nil) }
          }
        } else { result(true) }
      default: result(FlutterMethodNotImplemented)
      }
    }
  }

  private func entry(_ args: [String: Any], outgoing: Bool) -> Entry? {
    guard let callID = args["serverCallId"] as? String, !callID.isEmpty, callID.count <= 160,
          let id = (args["id"] as? String).flatMap(UUID.init(uuidString:)),
          let expires = LinliTenantCallPolicy.expiry(args["expiresAt"]), expires > Date() else { return nil }
    var extra: [String: Any] = ["serverCallId": callID]
    extra["tenantScope"] = args["tenantScope"]
    guard state.allows(extra) else { return nil }
    let scope = LinliTenantCallPolicy.scope(extra["tenantScope"])
    guard id == Self.callUUID(scope?.seed(callID) ?? callID) else { return nil }
    let entry = Entry(id: id, serverID: callID, extra: extra, video: args["mediaType"] as? String == "video",
      outgoing: outgoing, expires: min(expires, scope?.expires ?? expires))
    if let name = args["displayName"] as? String, !name.isEmpty { entry.displayName = String(name.prefix(100)) }
    if let handle = args["handle"] as? String, !handle.isEmpty { entry.handle = String(handle.prefix(100)) }
    return entry
  }

  // PushKit requires reporting a VoIP push promptly. Invalid/old pushes report
  // only an anonymous random ID and immediately end; no business action/name
  // is exposed, and the current user's other calls are never ended.
  func receivePush(_ body: [String: Any], completion: @escaping () -> Void) {
    let scope = state.managed ? LinliTenantCallPolicy.incoming(state.value, payload: body) : nil
    let callID = body["callId"] as? String ?? body["serverCallId"] as? String ?? ""
    let legacyAllowed = !state.managed && body["tenantId"] == nil && body["localUserId"] == nil
    guard (scope != nil || legacyAllowed), !callID.isEmpty, callID.count <= 160 else {
      reportUnavailable(completion); return
    }
    let pushedExpiry = LinliTenantCallPolicy.expiry(body["expiresAt"]) ?? Date().addingTimeInterval(30)
    let expiry = min(Date().addingTimeInterval(30), min(pushedExpiry, scope?.expires ?? pushedExpiry))
    let id = Self.callUUID(scope?.seed(callID) ?? callID)
    if let existing = entries[id], state.allows(existing.extra) {
      // Still report the same UUID to account for this delivery. A duplicate
      // report error must not terminate the original legitimate call.
      provider.reportNewIncomingCall(with: id, update: callUpdate(existing)) { [weak self] error in
        DispatchQueue.main.async {
          if error == nil, let self = self, self.entries[id] == nil {
            self.provider.reportCall(with: id, endedAt: Date(), reason: .failed)
          }
          completion()
        }
      }
      return
    }
    guard expiry > Date(), entries.isEmpty else { reportUnavailable(completion); return }
    var extra: [String: Any] = ["serverCallId": callID]
    if let scope = scope { extra["tenantScope"] = scope.fields }
    let entry = Entry(id: id, serverID: callID, extra: extra,
      video: body["mediaType"] as? String == "video", outgoing: false, expires: expiry)
    entries[id] = entry
    guard saveLedger() else { entries.removeValue(forKey: id); reportUnavailable(completion); return }
    report(entry) { _ in completion() }
  }

  private func reportUnavailable(_ completion: @escaping () -> Void) {
    let id = UUID()
    let update = CXCallUpdate()
    update.remoteHandle = CXHandle(type: .generic, value: "青蛙呱呱")
    update.localizedCallerName = "来电已失效"
    provider.reportNewIncomingCall(with: id, update: update) { [weak self] error in
      DispatchQueue.main.async {
        if error == nil { self?.provider.reportCall(with: id, endedAt: Date(), reason: .failed) }
        completion()
      }
    }
  }
  private func callUpdate(_ entry: Entry) -> CXCallUpdate {
    let update = CXCallUpdate()
    update.remoteHandle = CXHandle(type: .generic, value: entry.handle)
    update.localizedCallerName = entry.displayName
    update.hasVideo = entry.video
    update.supportsDTMF = false; update.supportsHolding = false
    update.supportsGrouping = false; update.supportsUngrouping = false
    return update
  }
  private func report(_ entry: Entry, completion: @escaping (Bool) -> Void) {
    entry.reporting = true
    provider.reportNewIncomingCall(with: entry.id, update: callUpdate(entry)) { [weak self] error in
      DispatchQueue.main.async {
        guard let self = self else { completion(false); return }
        entry.reporting = false
        guard self.entries[entry.id] === entry else {
          // The old report may finish after logout/cancel. Clean only its own
          // orphan; never remove a replacement record for the same call.
          if error == nil && self.entries[entry.id] == nil {
            self.provider.reportCall(with: entry.id, endedAt: Date(), reason: .failed)
          }
          completion(false); return
        }
        guard error == nil else {
          self.entries.removeValue(forKey: entry.id); _ = self.saveLedger(); completion(false); return
        }
        guard self.entries[entry.id] === entry, self.state.allows(entry.extra), entry.expires > Date() else {
          self.finish(entry.id, reason: .failed)
          // Identity changes can remove the record before reporting completes.
          self.provider.reportCall(with: entry.id, endedAt: Date(), reason: .failed)
          completion(false); return
        }
        self.arm(entry, seconds: entry.expires.timeIntervalSinceNow, event: "timeout")
        self.emit(entry, "restore")
        completion(true)
      }
    }
  }
  private func current(_ args: [String: Any]) -> Entry? {
    guard let id = (args["id"] as? String).flatMap(UUID.init(uuidString:)),
          let entry = entries[id], state.allows(entry.extra) else { return nil }
    return entry
  }
  private func emit(_ entry: Entry, _ type: String, muted: Bool? = nil) {
    guard state.allows(entry.extra) else { return }
    var action = entry.action(type)
    if let muted = muted { action["muted"] = muted }
    pending.append(action)
    if pending.count > 12 { pending.removeFirst(pending.count - 12) }
    channel?.invokeMethod("systemCallAction", arguments: action)
  }
  private func arm(_ entry: Entry, seconds: TimeInterval, event: String) {
    entry.deadline?.cancel()
    let timer = DispatchWorkItem { [weak self, weak entry] in
      guard let self = self, let entry = entry, self.entries[entry.id] === entry else { return }
      self.finish(entry.id, reason: .unanswered, event: event)
    }
    entry.deadline = timer
    DispatchQueue.main.asyncAfter(deadline: .now() + max(0, seconds), execute: timer)
  }
  private func finish(_ id: UUID, reason: CXCallEndedReason, event: String? = nil) {
    guard let entry = entries.removeValue(forKey: id) else { return }
    pending.removeAll { ($0["systemCallId"] as? String)?.lowercased() == id.uuidString.lowercased() }
    entry.deadline?.cancel()
    entry.answer?.fail()
    entry.answer = nil
    provider.reportCall(with: id, endedAt: Date(), reason: reason)
    _ = saveLedger()
    if let event = event { emit(entry, event) }
  }
  private func endInvalidCalls() {
    if let current = LinliTenantCallPolicy.scope(state.value["scope"]), current.expires > Date() {
      for entry in entries.values {
        if let old = LinliTenantCallPolicy.scope(entry.extra["tenantScope"]), current.sameSession(old) {
          // A confirmed same-session renewal may keep an ongoing call alive.
          // It does not revive a saved action from another login or identity.
          entry.extra["tenantScope"] = current.fields
        }
      }
    }
    for entry in Array(entries.values) where !state.allows(entry.extra) { finish(entry.id, reason: .failed) }
    pending.removeAll { action in
      if !state.managed { return action["tenantScope"] != nil }
      guard let scope = action["tenantScope"] else { return true }
      return !state.allows(["tenantScope": scope])
    }
    _ = saveLedger()
  }
  private func saveLedger() -> Bool {
    do { try LinliTenantCallState.write(entries.values.map { $0.saved }, to: ledger); return true }
    catch { return false }
  }
  private func restoreLedger() {
    guard let data = try? Foundation.Data(contentsOf: ledger), data.count <= 32768,
          let saved = try? JSONSerialization.jsonObject(with: data) as? [[String: Any]], saved.count <= 8 else { return }
    for item in saved {
      guard let id = (item["id"] as? String).flatMap(UUID.init(uuidString:)),
            let callID = item["serverCallId"] as? String, !callID.isEmpty,
            let extra = item["extra"] as? [String: Any], state.allows(extra),
            let expires = LinliTenantCallPolicy.expiry(item["expiresAt"]), expires > Date(),
            item["outgoing"] as? Bool == false else { continue }
      let scope = LinliTenantCallPolicy.scope(extra["tenantScope"])
      guard id == Self.callUUID(scope?.seed(callID) ?? callID) else { continue }
      let entry = Entry(id: id, serverID: callID, extra: extra, video: item["video"] as? Bool == true,
        outgoing: false, expires: expires)
      entry.accepted = item["accepted"] as? Bool == true
      entries[id] = entry
    }
  }
  private static func callUUID(_ seed: String) -> UUID {
    var bytes = Array(SHA256.hash(data: Foundation.Data(seed.utf8)).prefix(16))
    bytes[6] = (bytes[6] & 0x0f) | 0x50; bytes[8] = (bytes[8] & 0x3f) | 0x80
    let parts = [bytes[0..<4], bytes[4..<6], bytes[6..<8], bytes[8..<10], bytes[10..<16]]
    return UUID(uuidString: parts.map { $0.map { String(format: "%02x", $0) }.joined() }.joined(separator: "-"))!
  }
  private func configureAudio() throws {
    let rtc = RTCAudioSession.sharedInstance()
    rtc.lockForConfiguration()
    defer { rtc.unlockForConfiguration() }
    let audio = AVAudioSession.sharedInstance()
    try audio.setCategory(.playAndRecord, mode: .voiceChat, options: [.allowBluetooth, .defaultToSpeaker])
    try audio.setPreferredSampleRate(48000)
    try audio.setPreferredIOBufferDuration(0.005)
  }
  func providerDidReset(_ provider: CXProvider) {
    for entry in Array(entries.values) { finish(entry.id, reason: .failed, event: "end") }
  }
  func provider(_ provider: CXProvider, perform action: CXStartCallAction) {
    guard let entry = entries[action.callUUID], state.allows(entry.extra) else { action.fail(); return }
    do { try configureAudio() } catch { action.fail(); finish(entry.id, reason: .failed, event: "end"); return }
    provider.reportOutgoingCall(with: entry.id, startedConnectingAt: Date())
    provider.reportCall(with: entry.id, updated: callUpdate(entry))
    arm(entry, seconds: entry.expires.timeIntervalSinceNow, event: "timeout")
    action.fulfill()
  }
  func provider(_ provider: CXProvider, perform action: CXAnswerCallAction) {
    guard let entry = entries[action.callUUID], state.allows(entry.extra), entry.expires > Date() || entry.connected
    else { action.fail(); finish(action.callUUID, reason: .failed); return }
    do { try configureAudio() } catch { action.fail(); finish(entry.id, reason: .failed, event: "end"); return }
    entry.accepted = true
    guard saveLedger() else { action.fail(); finish(entry.id, reason: .failed, event: "end"); return }
    entry.deadline?.cancel()
    if entry.connected { action.fulfill(); return }
    entry.answer?.fail()
    entry.answer = action
    arm(entry, seconds: 10, event: "end")
    emit(entry, "accept") // Fulfilled only after business/LiveKit connection.
  }
  func provider(_ provider: CXProvider, perform action: CXEndCallAction) {
    if let entry = entries[action.callUUID] {
      finish(entry.id, reason: .remoteEnded, event: entry.accepted || entry.outgoing ? "end" : "decline")
    }
    action.fulfill()
  }
  func provider(_ provider: CXProvider, perform action: CXSetMutedCallAction) {
    guard let entry = entries[action.callUUID], state.allows(entry.extra) else { action.fail(); return }
    entry.muted = action.isMuted
    emit(entry, "mute", muted: action.isMuted)
    action.fulfill()
  }
  func provider(_ provider: CXProvider, timedOutPerforming action: CXAction) {
    if let call = action as? CXCallAction { finish(call.callUUID, reason: .failed, event: "end") }
  }
  func provider(_ provider: CXProvider, didActivate audioSession: AVAudioSession) {
    RTCAudioSession.sharedInstance().audioSessionDidActivate(audioSession)
  }
  func provider(_ provider: CXProvider, didDeactivate audioSession: AVAudioSession) {
    RTCAudioSession.sharedInstance().audioSessionDidDeactivate(audioSession)
  }
}
