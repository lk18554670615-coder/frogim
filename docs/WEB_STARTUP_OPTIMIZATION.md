# Web 启动性能修复

2026-10-01 发现正式 Caddy 将整个 `/app/*` 设置为 `Cache-Control: no-store`，Nginx 中已有的字体缓存规则未在这个直接提供静态文件的入口生效。每次打开都会重复下载中文和 Emoji 字体，压缩后合计约 18.1 MB，另有主脚本约 3.56 MB 和 Chromium CanvasKit 约 2.29 MB。服务端读取资源的首字节约 13–16 毫秒，主要问题是资源不能保留在浏览器。分支回退也移除了提交 `1eb196c` 的启动界面与错误反馈。

修复使用整包内容摘要命名 `/app/releases/{16位摘要}/`。主脚本、引擎、字体、图片和 SDK 只在对应发布目录中提供，缓存一年且 immutable；内容变化生成不同地址，避免新旧代码混用。HTML、启动脚本及未版本化文件继续校验更新。字体内容与 Dart 业务代码保持原值，不删减字符或 Emoji。启动脚本不再等待旧 Flutter Service Worker，清退仅限当前 `/app/` 的旧 Flutter worker，保留 Web Push、登录存储和其他应用的 worker。

HTML 提前加载主脚本和中文字体，主脚本优先；发布包提供 gzip sidecar，减少动态压缩和主脚本传输量。恢复加载状态和重试入口，缓慢加载不会在固定时间后假报失败；只在 Flutter 的首帧事件后移除启动界面。内置浏览器控制连续两次超时，本次没有取得真实浏览器首屏计时，不把 HTTP 耗时或模拟启动测试当作真实首屏通过。

仅运行启动脚本和打包的针对性测试，并完成生产 Web 构建。校验全部字体路径、gzip 解压一致、固定生产平台地址及自托管渲染引擎。发布只更新 Web 文件与 Caddy 静态规则，保留之前的页面和配置用于回退，不重启 API、IM、平台或数据服务，不修改账号及数据。本轮首次访问仍需下载完整字体；主要消除后续打开时的重复传输。

后续 Web 构建完成后，用 `tools/package-web-release.py` 将原始 Flutter 输出包装到另一个新目录。Docker Web 构建已接入这一阶段。Caddy 直接提供文件时须使用 `infra/templates/web-static.caddy.template` 的分版本缓存规则；Nginx 内部规则不能替代外部 Caddy 的规则。旧发布目录保留，不能覆盖同一内容地址下的文件。服务器热更新使用 `infra/scripts/publish-web-static.py` 的 prepare、publish 阶段，先验证内容摘要和候选配置；发布失败仅恢复 Web 页面与 Caddy 配置，不回退认证或数据库。
