# WuKongIM 协议探针

探针直接依赖固定服务端提交 `a888f895...` 内的官方 TCP 客户端，实现以下最小验收：

- 通过固定 OpenAPI 字段创建两个 Token 和群频道；
- 两个真实 TCP 客户端完成加密握手；
- 发送方收到包含 `message_id` 和 `message_seq` 的 ACK；
- 接收方收到同一条消息并自动提交 RecvACK。
- 校验业务登录、好友、单聊、群聊、离线同步、消息扩展和提醒 DataSource；
- 校验 `wk.plugin.im-policy` 已注册，成员消息允许且非成员消息由真实 TCP ACK 拒绝；
- 使用固定 Manager Token 调用独立 5300 `/cluster/nodes` 和 `/plugins`，证明管理面鉴权与插件状态真实可用；
- 使用固定官方 `wkproto` 编解码器让同一账号以 App、Web、PC 三种 device flag 同时连接，校验同一消息 ID 多设备同步；
- 调用固定 `/user/device_quit` 精确踢出 Web，校验 `ReasonConnectKick`，同时确认 App/PC 继续收消息；
- 真实创建 LiveKit 房间、签发两名参与者 Token并清理房间。

运行本地 WuKong Compose 后执行：

```powershell
$env:IM_WUKONG_MANAGER_TOKEN = "..."
go run ./tools/wukong-probe -api http://127.0.0.1:5001 -manager-api http://127.0.0.1:5001 -tcp tcp://127.0.0.1:5100 `
  -business-api http://127.0.0.1:8080 -manager-token $env:IM_WUKONG_MANAGER_TOKEN -timeout 120s
```

Production Compose intentionally requires the probe to use the business API so
that every TCP send is checked against a real PostgreSQL-backed friendship or
channel. Supply a short-lived `WUKONG_PROBE_OTP` when running the opt-in probe;
the value is not stored in the image or Compose file.

宿主机命令将 Manager 校验复用到同样注册了集群/插件路由的 5001；正式 Compose 的 `probe` profile 明确使用内部 `http://wukongim:5300`，5300 不映射到宿主机或公网。

探针不使用自制协议编码器；所有 TCP frame 均由固定版本官方客户端或官方 `WKProto` 编解码。WSS 仍由 Web JS SDK 与 macOS Easy SDK目标端探针覆盖。
