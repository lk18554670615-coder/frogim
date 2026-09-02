import Flutter
import AVFAudio
import AudioToolbox
import UserNotifications
import CallKit
import CryptoKit
import PushKit
import UIKit
import flutter_callkit_incoming

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate,
  PKPushRegistryDelegate, CallkitIncomingAppDelegate {
  private var voipRegistry: PKPushRegistry?
  private var screenshotChannel: FlutterMethodChannel?
  private var screenshotObserver: NSObjectProtocol?
  private var messageFeedbackChannel: FlutterMethodChannel?
  private var messageSound: SystemSoundID = 0

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    let registry = PKPushRegistry(queue: .main)
    registry.delegate = self
    registry.desiredPushTypes = [.voIP]
    voipRegistry = registry
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    guard let registrar = engineBridge.pluginRegistry.registrar(
      forPlugin: "LinliScreenshotDetection"
    ) else {
      return
    }
    let feedback = FlutterMethodChannel(
      name: "com.fd.kuailiao/message_feedback",
      binaryMessenger: registrar.messenger()
    )
    messageFeedbackChannel?.setMethodCallHandler(nil)
    messageFeedbackChannel = feedback
    if messageSound != 0 {
      AudioServicesDisposeSystemSoundID(messageSound)
      messageSound = 0
    }
    let soundKey = registrar.lookupKey(forAsset: "assets/sounds/message.wav")
    if let path = Bundle.main.path(forResource: soundKey, ofType: nil) {
      AudioServicesCreateSystemSoundID(URL(fileURLWithPath: path) as CFURL, &messageSound)
    }
    feedback.setMethodCallHandler { [weak self] call, result in
      guard call.method == "play" else {
        result(FlutterMethodNotImplemented)
        return
      }
      let args = call.arguments as? [String: Any] ?? [:]
      UNUserNotificationCenter.current().getNotificationSettings { settings in
        DispatchQueue.main.async {
          guard let self = self, UIApplication.shared.applicationState == .active,
                settings.authorizationStatus == .authorized else {
            result(nil)
            return
          }
          let sound = args["sound"] as? Bool == true && settings.soundSetting == .enabled
          let vibration = args["vibration"] as? Bool == true
          // System Sound Services respects the ringer switch and does not
          // replace the audio session used by voice recording or LiveKit.
          if sound && self.messageSound != 0 {
            if vibration {
              AudioServicesPlayAlertSound(self.messageSound)
            } else {
              AudioServicesPlaySystemSound(self.messageSound)
            }
          } else if vibration {
            AudioServicesPlaySystemSound(kSystemSoundID_Vibrate)
          }
          result(nil)
        }
      }
    }
    let channel = FlutterMethodChannel(
      name: "com.fd.kuailiao/screenshot",
      binaryMessenger: registrar.messenger()
    )
    screenshotChannel = channel
    channel.setMethodCallHandler { [weak self] call, result in
      switch call.method {
      case "start":
        self?.startScreenshotDetection()
        result(["supported": true])
      case "stop":
        self?.stopScreenshotDetection()
        result(nil)
      default:
        result(FlutterMethodNotImplemented)
      }
    }
  }

  private func startScreenshotDetection() {
    guard screenshotObserver == nil else { return }
    screenshotObserver = NotificationCenter.default.addObserver(
      forName: UIApplication.userDidTakeScreenshotNotification,
      object: nil,
      queue: .main
    ) { [weak self] _ in
      self?.screenshotChannel?.invokeMethod(
        "detected",
        arguments: ["occurredAt": Int64(Date().timeIntervalSince1970 * 1_000)]
      )
    }
  }

  private func stopScreenshotDetection() {
    guard let observer = screenshotObserver else { return }
    NotificationCenter.default.removeObserver(observer)
    screenshotObserver = nil
  }

  func pushRegistry(
    _ registry: PKPushRegistry,
    didUpdate credentials: PKPushCredentials,
    for type: PKPushType
  ) {
    guard type == .voIP else { return }
    let token = credentials.token.map { String(format: "%02x", $0) }.joined()
    SwiftFlutterCallkitIncomingPlugin.sharedInstance?.setDevicePushTokenVoIP(token)
  }

  func pushRegistry(_ registry: PKPushRegistry, didInvalidatePushTokenFor type: PKPushType) {
    guard type == .voIP else { return }
    SwiftFlutterCallkitIncomingPlugin.sharedInstance?.setDevicePushTokenVoIP("")
  }

  func pushRegistry(
    _ registry: PKPushRegistry,
    didReceiveIncomingPushWith payload: PKPushPayload,
    for type: PKPushType,
    completion: @escaping () -> Void
  ) {
    guard type == .voIP else {
      completion()
      return
    }
    let body = payload.dictionaryPayload
    let serverCallId = (body["callId"] as? String) ?? (body["serverCallId"] as? String) ?? ""
    guard !serverCallId.isEmpty else {
      completion()
      return
    }
    let systemCallId = (body["systemCallId"] as? String).flatMap(UUID.init(uuidString:))?.uuidString
      ?? deterministicCallUUID(serverCallId).uuidString
    let mediaType = body["mediaType"] as? String ?? "audio"
    let callerName = body["nameCaller"] as? String ?? "青蛙呱呱联系人"
    let handle = body["handle"] as? String ?? "青蛙呱呱"
    let data = flutter_callkit_incoming.Data(
      id: systemCallId,
      nameCaller: callerName,
      handle: handle,
      type: mediaType == "video" ? 1 : 0
    )
    data.appName = "青蛙呱呱"
    data.duration = 30_000
    data.includesCallsInRecents = false
    data.supportsDTMF = false
    data.supportsHolding = false
    data.supportsGrouping = false
    data.supportsUngrouping = false
    data.audioSessionMode = "voiceChat"
    data.audioSessionPreferredSampleRate = 48_000
    data.extra = [
      "serverCallId": serverCallId,
      "conversationId": body["conversationId"] as? String ?? "",
      "mediaType": mediaType,
    ]
    SwiftFlutterCallkitIncomingPlugin.sharedInstance?.showCallkitIncoming(
      data,
      fromPushKit: true,
      completion: completion
    )
  }

  // 插件已经先把动作投递给 Flutter；这里及时履行 CallKit 事务，避免系统判定失败。
  func onAccept(_ call: Call, _ action: CXAnswerCallAction) { action.fulfill() }
  func onDecline(_ call: Call, _ action: CXEndCallAction) { action.fulfill() }
  func onEnd(_ call: Call, _ action: CXEndCallAction) { action.fulfill() }
  func onTimeOut(_ call: Call) {}
  func didActivateAudioSession(_ audioSession: AVAudioSession) {}
  func didDeactivateAudioSession(_ audioSession: AVAudioSession) {}
  func providerDidReset() {}

  /// 与 Flutter、Android 使用相同算法，使重复 VoIP 推送复用同一个 CallKit UUID。
  private func deterministicCallUUID(_ value: String) -> UUID {
    var bytes = Array(SHA256.hash(data: Foundation.Data(value.utf8)).prefix(16))
    bytes[6] = (bytes[6] & 0x0f) | 0x50
    bytes[8] = (bytes[8] & 0x3f) | 0x80
    let groups = [bytes[0..<4], bytes[4..<6], bytes[6..<8], bytes[8..<10], bytes[10..<16]]
    let uuidString = groups
      .map { $0.map { String(format: "%02x", $0) }.joined() }
      .joined(separator: "-")
    return UUID(uuidString: uuidString)!
  }
}
