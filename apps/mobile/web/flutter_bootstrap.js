{{flutter_js}}
{{flutter_build_config}}

(function () {
  'use strict';
  var startup = window.frogimStartup;
  var resourceMeta = document.querySelector('meta[name="app-resource-base"]');
  var config = {};
  if (resourceMeta) {
    var base = new URL(resourceMeta.content, document.baseURI);
    if (base.origin !== window.location.origin) throw new Error('Invalid application resource origin');
    config.entrypointBaseUrl = base.href;
    config.assetBase = base.href;
    config.canvasKitBaseUrl = new URL('canvaskit/', base).href;
  }
  // The build preparation mirrors this engine's fallback font shards.
  // CanvasKit requests only shards needed by text actually being rendered.
  config.fontFallbackBaseUrl = new URL('font-fallbacks/', config.assetBase || document.baseURI).href;
  function fail() { startup?.fail(); }
  startup?.setStage('正在加载应用资源…');
  window._flutter.loader.load({
    config: config,
    onEntrypointLoaded: function (engineInitializer) {
      startup?.setStage('正在初始化应用…');
      engineInitializer.initializeEngine(config).then(function (appRunner) {
        return appRunner.runApp();
      }).catch(fail);
    }
  }).catch(fail);
})();
