import Flutter
import AVFAudio
import AudioToolbox
import UserNotifications
import PushKit
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate,
  PKPushRegistryDelegate {
  private lazy var systemCalls = LinliSystemCalls()
  private var voipRegistry: PKPushRegistry?
  private var voipObserver: NSObjectProtocol?
  private var screenshotChannel: FlutterMethodChannel?
  private var screenshotObserver: NSObjectProtocol?
  private var messageFeedbackChannel: FlutterMethodChannel?
  private var messageSound: SystemSoundID = 0

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    _ = systemCalls // The native provider must exist before any PushKit delivery.
    voipObserver = NotificationCenter.default.addObserver(forName: NSNotification.Name("LinliGetuiVoipChanged"), object: nil, queue: .main) { [weak self] _ in
      self?.systemCalls.invalidateVoipBinding()
    }
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
    systemCalls.attach(registrar.messenger())
    let feedback = FlutterMethodChannel(
      name: "top.hongjinghuanqiu.app/message_feedback",
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
      name: "top.hongjinghuanqiu.app/screenshot",
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
    systemCalls.voipToken = token
    GetuiflutPlugin.setVoipToken(credentials.token)
  }

  func pushRegistry(_ registry: PKPushRegistry, didInvalidatePushTokenFor type: PKPushType) {
    guard type == .voIP else { return }
    systemCalls.voipToken = ""
    GetuiflutPlugin.setVoipToken(Data())
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
    let body = payload.dictionaryPayload.reduce(into: [String: Any]()) { result, pair in
      if let key = pair.key as? String { result[key] = pair.value }
    }
    GetuiflutPlugin.handleVoipPayload(body)
    systemCalls.receivePush(LinliTenantCallPolicy.pushBody(body), completion: completion)
  }
}
