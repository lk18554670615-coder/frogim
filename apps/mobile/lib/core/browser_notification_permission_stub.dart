enum BrowserNotificationPermission { unsupported, prompt, granted, denied }

bool get browserNotificationsAvailable => false;

Future<BrowserNotificationPermission> browserNotificationPermission() async =>
    BrowserNotificationPermission.unsupported;

Future<BrowserNotificationPermission>
requestBrowserNotificationPermission() async =>
    BrowserNotificationPermission.unsupported;
