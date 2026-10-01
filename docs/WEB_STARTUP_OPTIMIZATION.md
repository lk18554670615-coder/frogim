# Web 启动性能修复

2026-10-01 发现正式 Caddy 将整个 `/app/*` 设置为 `Cache-Control: no-store`，Nginx 中已有的字体缓存规则未在这个直接提供静态文件的入口生效。每次打开都会重复下载中文和 Emoji 字体，压缩后合计约 18.1 MB，另有主脚本约 3.56 MB 和 Chromium CanvasKit 约 2.29 MB。服务端读取资源的首字节约 13–16 毫秒，主要问题是资源不能保留在浏览器。分支回退也移除了提交 `1eb196c` 的启动界面与错误反馈。

修复使用整包内容摘要命名 `/app/releases/{16位摘要}/`。主脚本、引擎、字体、图片和 SDK 只在对应发布目录中提供，缓存一年且 immutable；内容变化生成不同地址，避免新旧代码混用。HTML、启动脚本及未版本化文件继续校验更新。字体内容与 Dart 业务代码保持原值，不删减字符或 Emoji。启动脚本不再等待旧 Flutter Service Worker，清退仅限当前 `/app/` 的旧 Flutter worker，保留 Web Push、登录存储和其他应用的 worker。

HTML 提前加载主脚本和中文字体，主脚本优先；发布包提供 gzip sidecar，减少动态压缩和主脚本传输量。恢复加载状态和重试入口，缓慢加载不会在固定时间后假报失败；只在 Flutter 的首帧事件后移除启动界面。内置浏览器控制连续两次超时，本次没有取得真实浏览器首屏计时，不把 HTTP 耗时或模拟启动测试当作真实首屏通过。

仅运行启动脚本和打包的针对性测试，并完成生产 Web 构建。校验全部字体路径、gzip 解压一致、固定生产平台地址及自托管渲染引擎。发布只更新 Web 文件与 Caddy 静态规则，保留之前的页面和配置用于回退，不重启 API、IM、平台或数据服务，不修改账号及数据。本轮首次访问仍需下载完整字体；主要消除后续打开时的重复传输。

后续 Web 构建完成后，用 `tools/package-web-release.py` 将原始 Flutter 输出包装到另一个新目录。Docker Web 构建已接入这一阶段。Caddy 直接提供文件时须使用 `infra/templates/web-static.caddy.template` 的分版本缓存规则；Nginx 内部规则不能替代外部 Caddy 的规则。旧发布目录保留，不能覆盖同一内容地址下的文件。服务器热更新使用 `infra/scripts/publish-web-static.py` 的 prepare、publish 阶段，先验证内容摘要和候选配置；发布失败仅恢复 Web 页面与 Caddy 配置，不回退认证或数据库。

北京时间 2026-10-01 17:21:05 已发布。代码提交 `d4d8df3` 与 tag `release/web-startup-20261001` 已推送；静态版本为 `3fe64813fc454fdf`。运行时目录摘要 `16c40e30e37b3094b3a988f2f639f519d6ccccb4f7123a7485decc0f4f5fef59`，主 Dart 脚本摘要仍为 `fa2cc45aa30843fd51c4ad1bdae057d96fc78c75f81c558f059175b6d0edf9cd`，与优化前完全相同，业务代码未改。

实际入口验证：HTML 为 no-cache；版本化脚本、两种字体和 CanvasKit 为一年 immutable 缓存，gzip 可用，带 ETag 的二次校验均返回 `304`、响应体 `0` 字节。主脚本压缩传输由 3,564,576 降至 3,307,727 字节，减小约 7.2%；上述四项关键资源合计由 23,916,339 降至 22,597,168 字节。8 个容器均健康，启动时间和重启计数未变；平台与企业 readiness 正常。没有重复线上登录或音视频测试。4 个启动测试、1 个打包测试、实际 Web 构建及 gzip/字体路径校验通过。Windows 本机 Docker 未运行，未声称本机 Docker/Nginx 编译验收通过；生产 Caddy 候选配置验证通过。

本机脱敏证据为 `build/web-startup/release-evidence.json`，服务端回执为 `/data/frogim/releases/light-20261001-c83107e/ops/web-startup-3fe64813fc454fdf.json`。页面和网关的前一版本保存在同一发布根目录的 `backups/web-startup-3fe64813fc454fdf/`，只可用于本次 Web 热更新回退。原轻量架构部署回执与数据库恢复边界保持有效，24 小时只读观察继续按原截止时间执行。
## Web 版本号校正

2026-10-01 后续检查发现性能修复的 Flutter 构建沿用了 pubspec 的 `1.0.12+4016`，而平台 Web 策略最低及最新均为 `1.0.16`。新缓存地址下的 version.json 为 1.0.12，根路径残留 1.0.16，PackageInfo 优先读取 assetBase，导致最新代码也触发强制更新。重新使用显式 `--build-name 1.0.16 --build-number 8023` 构建；平台版本策略保持原值。

打包增加期望版本与构建号校验并记录到清单，Docker 支持独立的 WEB_BUILD_NAME / WEB_BUILD_NUMBER 参数。Web 热更新同步根路径版本文件，并核对运行时版本、根路径版本及平台公开策略一致；新版本不满足策略时撤回本次 Web 更新。只测试打包与版本校验并构建 Web，不重复认证、业务和音视频回归。版本化运行时创建新的资源地址，保留上一包供回退。

北京时间 2026-10-01 17:35:10 完成版本校正发布，代码 `649365a`，资源版本 `bae47298d59bde37`。运行时与根路径版本均为 `1.0.16+8023`，平台公开版本查询返回 forceUpdate=false、updateAvailable=false。业务脚本摘要与上次一致；8 个容器健康且均未重启，readiness 正常，缓存与压缩校验通过。回执 `/data/frogim/releases/light-20261001-c83107e/ops/web-startup-bae47298d59bde37.json`，本机记录 `build/web-version-fix/publish.log`。2 项打包针对性测试及显式版本 Web 构建通过；没有重复线上功能测试。
