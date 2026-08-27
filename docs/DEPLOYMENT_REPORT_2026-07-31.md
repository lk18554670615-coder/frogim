# 青蛙呱呱 IP 环境部署报告（2026-07-31 历史快照）

> 本文记录当日环境事实，不代表当前线上状态。文中的 `nexachat` 路径、命令、服务单元和下载文件名是历史兼容标识，使用前必须重新核验，且不能仅为品牌统一直接改名。

部署时间：2026-07-31（Asia/Shanghai）  
服务器：`<SERVER_IP>`  
管理后台：`https://<SERVER_IP>/`  
Android 验收包：`https://<SERVER_IP>/downloads/nexachat-https-test.apk`

## 部署结构

- Caddy 2.10：公网 80/443、HTTP 到 HTTPS 跳转、TLS、WSS、后台与对象存储反向代理。
- Go IM Server：单聊、群聊、好友、消息幂等、已读、撤回、离线同步、WSS、媒体上传、审核后台 API。
- PostgreSQL 17：持久化业务数据。
- Redis 8：实时/缓存层。
- MinIO：私有媒体对象存储，公网仅通过签名 HTTPS URL 访问。
- React 管理后台：只通过 Caddy 暴露。

数据库、Redis、MinIO 和管理后台均未直接开放公网端口。服务器仅监听 22、80、443。

## TLS 与续期

- 证书签发方：Let's Encrypt YE1。
- SAN：`IP Address:<SERVER_IP>`。
- 当前证书有效期：2026-07-31 至 2026-08-07。
- `nexachat-cert-renew.timer` 每日检查续期，成功后通过容器内环回管理接口热加载 Caddy。

## 真实环境验收

公网 HTTPS 环境已通过：

- 两个真实测试用户的验证码登录。
- 好友申请、接受、好友列表。
- 单聊创建、消息发送、重试幂等、历史消息、已读和撤回。
- 群聊创建、成员列表和群消息。
- HTTPS 媒体预签名、上传到私有 MinIO、完成校验。
- 离线同步游标。
- WSS `101 Switching Protocols`。
- 管理员密码、TOTP 和后台仪表盘。
- 未授权后台 API 返回 401，内部指标路径对公网返回 404。
- HTTP 301 跳转 HTTPS，Android 包支持 Range 下载。

自动备份已真实执行，PostgreSQL 转储、MinIO 对象和 `SHA256SUMS` 均校验通过。`nexachat-backup.timer` 每日执行。

## 配置和凭据

- 集中配置：`/data/linli-im/shared/config.env`（root 600；`/opt/nexachat/shared` 仅保留兼容软链接）。
- 初始凭据：`/data/linli-im/shared/initial-credentials.txt`（root 600）。
- 证书：`/data/linli-im/shared/letsencrypt`。
- 备份：`/opt/nexachat/backups`。
- 发布版本：`/opt/nexachat/releases/20260731T124753Z`。
- 当前版本软链接：`/opt/nexachat/current`。

常用命令：`nexachat status`、`nexachat logs 200 server`、`nexachat deploy`、`nexachat smoke`、`nexachat backup`、`nexachat renew-cert`。

## 安装包

- 公网验收 APK SHA-256：`1d7b48e7ade904f3acc257c2a31ff0d934a2594fb6a2018263e442768aa108cb`。
- 该包采用 Android Debug 证书签名，仅用于当前真实环境安装验收，不能提交应用商店。
- 商店包必须使用用户持有并永久备份的 Android 上传密钥重新签名；iOS 真机/App Store 包必须使用用户的 Apple Developer Team 与发布证书归档。

## 上线前仍需外部资料

当前后端为带真实数据库/Redis/MinIO 的公网验收环境，但手机验证码仍是集中配置中的测试验证码，推送提供方为日志模式。面向公众上线前必须补齐：短信服务商、APNs/FCM 凭据、商店签名、隐私政策/用户协议和账号注销流程，并把后端从开发验证码模式切到生产网关模式。
