# 移动端发布记录：1.0.17

## 代码与构建来源

- 代码来自 `main`：`30767f3926ecbdce9e806231a7cbd75e36db24a4`，包含轻量多租户及注册企业码选填。
- Gitee、GitHub 的 `main` 已同步。同步前的 GitHub main 保存在 `codex/github-main-before-light-20261001`。
- Flutter 3.44.8；统一平台认证地址 `https://18.163.165.233/platform`，后续业务地址由平台返回。
- Android 按用户最终要求在 Windows / PowerShell 7.6.5 本地打包 arm64，未使用 GitHub 的通用 APK 发布。
- iOS 使用 GitHub `main` 的签名任务：[运行 #46](https://github.com/lk18554670615-coder/frogim/actions/runs/36877666372)，签名 IPA job `110421167324` 成功。
- 个推客户端参数复用已有发布配置，App ID、App Key 已与现网核对一致；GitHub Secrets 使用加密接口写入，凭据不提交。

## Android 发布

- 版本 `1.0.17`，Flutter 构建号 `8025`。Flutter 的 arm64 分 ABI 打包添加 2000 偏移，APK 实际 Android versionCode 为 `10025`。
- 包名 `top.hongjinghuanqiu.app`；仅包含 `arm64-v8a`。
- 文件 `qingwaguagua-1.0.17-8025-arm64.apk`，77,809,424 字节。
- SHA-256：`19cd71708f50d2db9b665fe59abaea70c16746e5294fbf570a00e32c83eb5ac1`。
- 签名证书 SHA-256：`11fcd730e1fcf1e1fcdb7947b615a51179a4794d30e59000c38a19106a43072e`，与原发布签名一致；apksigner 验证 v2、v3 签名通过。
- 已核对生产平台地址存在于 APK；未发现本机验收地址 `127.0.0.1:18700`、`127.0.0.1:18780`、`localhost:18700`。
- 本机副本：`C:/Users/lee/Downloads/qingwaguagua-1.0.17-8025-arm64.apk`。
- 下载入口：[Android arm64 APK](https://18.163.165.233/downloads/qingwaguagua-1.0.17-8025-arm64.apk)。
- 2026-10-01 22:50 北京时间，经平台管理审计接口发布：最低及最新版本均为 `1.0.17`，开启强制更新。保留发布前策略及发布回执。
- 平台及企业 A、B 的版本查询均验证：`1.0.16` 要求更新，`1.0.17` 不要求更新；iOS 和 Web 策略未改变。

## iOS 签名与下载

- IPA artifact `11170083874` / `ios-signed-46`，GitHub ZIP 摘要 `02fdcc81ea9402dced8d610845873db50f65578f064d7a83c76e53b2fbfec417`。
- CI 的定向认证测试、Apple 签名配置检查、IPA 构建、`codesign --verify --deep --strict` 与导出版本检查通过。
- 最终文件已通过本机代理下载并核对 ZIP 摘要，保存到 `C:/Users/lee/Downloads/qingwaguagua-1.0.17-8025-signed.ipa`，44,867,174 字节。
- IPA SHA-256：`bc43faf0dc8d5576f6c823cfcb497f81d3e6a05123d4b35ec1887d8ffce6c038`。
- 本地检查版本 `1.0.17`、构建号 `8025`、包名 `top.hongjinghuanqiu.app` 及签名文件通过。描述文件为 Ad Hoc，登记 99 台设备，有效期至 2027-09-05；未登记的设备不能安装。
- 本地下载和检查回执：`build/github-mobile-release-20261001/ipa-download.json`、`ipa-inspection.json`。

## 验证边界及证据

- 本地 arm64 编译成功，用时 223 秒。日志、包信息、签名及摘要见 `build/github-mobile-release-20261001/local-*`。
- 仅执行受影响的定向认证检查，没有全量回归；音视频按约定跳过，安装运行及推送实收待真机验收。
- 第 46 次运行的 Android CI job 因阿里云 Maven 下载 Kotlin 依赖返回 HTTP 502 失败。该任务不是发布包来源；本地 arm64 编译和签名检查已通过。不要把整个第 46 次运行记为成功。
- 第 44 次运行是缺少个推客户端配置的候选产物，不用于最终发布。
- 服务器回执目录：`/data/frogim/releases/light-20261001-c83107e/ops/android-1.0.17-8025/`，包含 `policy-before.json`、`published.json`。
- 本次仅上架 Android 文件并更新 Android 版本策略，没有修改平台或企业运行配置，没有启用持续观察。
