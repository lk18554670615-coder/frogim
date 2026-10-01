(function () {
  'use strict';
  var finished = false;
  var slowTimer;
  function element(id) { return document.getElementById(id); }
  function setStage(message) {
    if (!finished && element('app-startup-status')) {
      element('app-startup-status').textContent = message;
    }
  }
  function fail() {
    if (finished) return;
    window.clearTimeout(slowTimer);
    element('app-startup')?.classList.add('app-startup--error');
    setStage('应用加载失败，请检查网络后重新加载。');
  }
  function ready() {
    finished = true;
    window.clearTimeout(slowTimer);
    element('app-startup')?.remove();
  }
  window.frogimStartup = { fail: fail, ready: ready, setStage: setStage };
  window.addEventListener('flutter-first-frame', ready, { once: true });
  window.addEventListener('error', function (event) {
    if (event.error || event.target?.tagName === 'SCRIPT') fail();
  }, true);
  window.addEventListener('unhandledrejection', fail);
  document.addEventListener('DOMContentLoaded', function () {
    element('app-startup-retry')?.addEventListener('click', function () {
      window.location.reload();
    });
  });
  // Retire only this application's obsolete Flutter worker. Preserve Web Push
  // registrations, login storage, and workers belonging to other applications.
  if (navigator.serviceWorker?.getRegistrations) {
    var oldWorker = new URL('flutter_service_worker.js', document.baseURI).href;
    navigator.serviceWorker.getRegistrations().then(function (registrations) {
      registrations.forEach(function (registration) {
        var worker = registration.active || registration.waiting || registration.installing;
        if (worker?.scriptURL === oldWorker) registration.unregister();
      });
    }).catch(function () {});
  }
  slowTimer = window.setTimeout(function () {
    setStage('网络较慢，仍在加载资源。首次打开需稍候，也可重新加载。');
    element('app-startup')?.classList.add('app-startup--slow');
  }, 15000);
})();
