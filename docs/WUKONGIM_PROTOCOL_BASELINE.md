# WuKongIM 精准协议基线

更新时间：2026-08-12

## 权威顺序

协议判断固定按以下顺序：WuKongIM Server `v2.2.5-20260422` 源码与该提交内的 OpenAPI、官方对应版本 SDK、官方文档、唐僧叨叨产品行为、当前项目。若低优先级资料与高优先级资料冲突，以高优先级为准并增加回归测试。

固定产物和校验见 `third_party/wukongim/versions.lock.json`。该 Server 标签是 annotated tag：标签对象为 `e3498a9d...`，解引用后的代码提交为 `a888f895...`。

## 端口与信任边界

| 端口/入口 | 调用方 | 暴露范围 | 用途 |
|---|---|---|---|
| Go HTTPS API | 四端客户端、管理后台 | 公网 TLS | 业务认证、资料、扩展、媒体和运营能力 |
| WuKong TCP 5100 | Android/iOS | 公网 TCP | 原生客户端消息长连接 |
| WuKong WSS | Web/macOS | 公网经 Caddy | 浏览器和桌面消息长连接 |
| WuKong HTTP 5001 | Go 服务 | Compose 内网 | 用户、频道、订阅者、消息和会话管理 |
| WuKong Manager 5300 | Go 服务、受控运维 | 仅内网 | 指标、节点、连接、插件和诊断 |
| WuKong Webhook gRPC | WuKongIM | Compose 内网 | 在线状态、离线推送、消息通知 |
| LiveKit signal/WSS | 四端客户端 | 公网 TLS | RTC 信令 |
| LiveKit RTC UDP/TCP | 四端客户端 | 必需公网端口 | 音视频和屏幕共享媒体 |

Manager Token、WuKong内部 API Token、LiveKit API Secret 不得返回浏览器。单节点是当前部署事实，不宣称高可用。

固定源码 `ConfigureWithViper` 从键 `managerToken` 读取配置，`SetEnvPrefix("wk")` 后对应的环境变量必须精确写成 `WK_MANAGERTOKEN`。`WK_MANGERTOKEN` 少一个 `A`，不会启用5001/5300的Manager Token校验。两套Compose和生产冒烟已固定正确名称；冒烟同时要求5001无Token返回401、带Token的5001健康检查和5300节点查询成功。

## 频道与设备常量

频道类型：`1` 单聊、`2` 群聊、`3` 客服、`4` 社区、`5` 社区话题、`6` 资讯、`9` 直播、`10` 访客。`11/12` 只保留常量，不注册 AI 插件、不提供产品入口。

设备标识：`0` App、`1` Web、`2` Desktop。Android/iOS 使用 App；Web 使用 Web；macOS 使用 Desktop。客户端每次登录必须使用业务 API 返回的短期 `ImSession`，不自行生成 uid/token。

## 消息正文

官方内置正文类型：`1` 文本、`2` 图片、`3` GIF、`4` 语音、`5` 视频、`6` 位置、`7` 名片、`8` 文件。

业务正文类型：`1001` 合并聊天记录、`1002` 系统事件、`1003` 商店贴纸、`1004` 朋友圈分享、`1005` 通话事件、`1006` 直播互动、`1007` 客服事件、`1008` 截屏提示。所有 `1001–1008` 正文必须包含整数 `schemaVersion: 1`，解码器对未知版本显示“当前版本暂不支持”，不得丢弃原始正文。

编辑、撤回、回应、置顶、送达/已读扩展存储在 PostgreSQL；原消息正文不物理改写。服务端先持久化扩展，再向目标频道发送 CMD，客户端收到 CMD 后按扩展版本增量同步。CMD 是失效通知，不是扩展数据的唯一副本。

固定 v2.2.5 同时存在旧消息帧流字段（`streamNo/streamId/streamFlag`）和消息事件 API。新业务只使用后者：`/message/send` 的 `is_stream=1` 创建锚点，`/message/event` 追加事件，`/message/eventsync` 增量恢复。固定服务端依赖的 WuKongIMGoProto v1.2.3 明确定义 `SettingStream = 1 << 1`（2）；`1 << 5`（32）是 Signal。实时 EventPacket 的协议帧类型是12（10/11是SUB/SUBACK），依次编码事件 ID、类型、int64时间戳和 JSON 数据；协议v2–v4的旧流接收字段顺序为`flag/string no/uint64 stream id`，v5不再编码这些字段。`event_id` 是幂等键；`stream.finish` 强制事件键 `__finish__`。业务 API 只开放 public 文本流，AI Agent 仍禁用。

## 数据所有权

| 数据 | 唯一事实源 | 说明 |
|---|---|---|
| 消息正文、message_id、channel_seq、最近会话、离线消息 | WuKongIM | PostgreSQL不得保留第二份唯一消息正文 |
| 账号、好友、群权限、频道资料 | PostgreSQL | 事务加持久 Outbox 同步到 WuKongIM |
| 消息编辑/撤回/回应/提醒等扩展 | PostgreSQL | 版本化、幂等，通过 CMD 失效 |
| 朋友圈、表情、客服、RTC、审核、运营 | PostgreSQL | 业务服务负责权限和审计 |
| 附件和媒体 | MinIO | 消息只保存经过鉴权的对象引用和元数据 |
| 缓存、租约、限流 | Redis | 不保存任何唯一消息副本 |

## 可靠性不变量

1. 客户端生成稳定且唯一的 `client_msg_no`；重试复用同一编号，服务端和客户端据此去重。
2. 只有 SDK 收到 WuKong ACK 后消息才从“发送中”进入“已发送”；断网或超时不得伪装成功。
3. 恢复连接后先由 SDK补齐频道序号，再同步业务扩展；扩展版本落后时消息仍可展示原文。
4. Webhook以事件唯一键幂等处理，重复投递返回成功；副作用由本地事务/Outbox控制。
5. PostgreSQL业务操作成功但 WuKong同步未完成时，对外返回 `processing`，由 Reconciler重试，不能回滚已提交业务事实或伪装同步完成。
6. 删除会话只改变该用户最近会话状态，不删除频道消息。撤回和过期均保留审计事实。

## 平台实现边界

Android/iOS 使用完整 Flutter SDK 1.7.9。Web 使用 JS SDK 1.3.5 并通过 `dart:js_interop` 隔离。macOS 使用 Easy SDK 1.0.3 和 Desktop 设备标识，Gateway负责解码 JSON-RPC Base64消息/CMD；历史、最近会话、提醒、扩展和加密缓存由业务 DataSource与现有本地缓存补齐。三种实现只存在于 `WukongGateway` 下层，页面和 `ImRepository` 不依赖平台 SDK 类型。

LiveKit 2.7.0 仅处理 RTC。截屏事件只在 iOS/Android真实检测；Web/macOS明确显示平台不支持，不产生伪事件。

## 已执行的固定版本探针

2026-08-11 在隔离的本地 Compose 数据卷中执行 `tools/wukong-probe`，目标镜像摘要与本文件固定版本一致，得到以下可复现结果：

- 两个官方协议客户端完成加密 TCP 握手，消息 ACK 返回非零 `message_id` 和 `message_seq`，接收端消息 ID/序号与 ACK 一致。
- `/v2/auth/login` 为 Android/iOS 返回 `wukongimfluttersdk` 会话，为 macOS 返回 `wukong_easy_sdk` 会话；业务 uid/token可直接完成对应 TCP/WSS握手，macOS使用 device flag 2。

2026-08-11在本地固定镜像上执行 `apps/mobile/tool/wukong_easy_probe.dart`：macOS会话通过 Easy SDK 1.0.3完成 WSS握手，成功接收服务端存储消息，并发送一条获得有效 messageId/messageSeq的消息。随后数据库确认该消息原生 `expire=0` 时，正文绝对 `expiresAt` 仍被 Webhook写入 schema 43到期索引。扩展探针后再次运行，Desktop device flag会话实时收到事件`easy-stream-60442653-delta`，类型严格为`stream.delta`且正文包含`EASY_CUSTOM_EVENT_PASS`，证明Easy SDK的`customEvent`路径与固定服务端真实互通。

2026-08-12在同一固定镜像上扩展并重跑协议探针：同一业务账号分别取得 Android、Web、macOS会话，以 App、Web、PC 三种官方 device flag同时连接；三条连接收到相同WuKong消息ID。调用 `/user/device_quit` 仅退出Web后，该连接收到精确`ReasonConnectKick`，Android和macOS连接仍继续收到下一条相同消息。探针同时真实创建LiveKit房间、签发两名参与者短期Token并完成房间清理。

2026-08-12修正Manager环境变量名后重建本地固定容器：未带Token访问5001 `/health`返回401；5300 `/cluster/nodes`返回唯一节点1001，`/plugins`返回`wk.plugin.im-policy`状态1。扩展后的容器探针明确连接独立5300并输出`managerApi=true`，同时重跑全部握手、ACK、离线/历史、扩展、CMD、LiveKit、三设备同步和精确踢出门槛，消息ID `2087279428873654272`、频道序号1，所有布尔门槛均通过。随后经Go后台代理查询，插件被归一化为`normal`；浏览器只收到脱敏结果，Manager Token不下发。

2026-08-12使用浏览器中的官方JS SDK 1.3.5完成真实Web WSS登录、好友单聊发送与ACK；消息由业务历史接口按同一WuKong消息ID和频道序号回读，控制台无错误。修复会话失效事件自递归后，服务端不再出现`/v2/channels/conversations`请求风暴和429。

2026-08-12使用Android 15/API 35目标模拟器运行官方Flutter SDK 1.7.9：App device flag通过TCP取得`ConnackPacket`，同步到上述Web消息；Android发送`Android_WuKongIM_e2e_2026-08-12`取得`SendAck reason=1`，业务历史按同一WuKong消息ID `2087225148561068032`和频道序号2回读。官方SDK源码在每次连接后以`msg_count=200`调用会话DataSource，业务代理上限已与该固定行为对齐并增加200通过/201拒绝的契约测试。本地Android Studio模拟器通过独立Compose覆盖下发`10.0.2.2:5100`，生产仍只允许部署环境下发真实公网地址。

同日断服测试发现官方1.7.9 `_WKSocket.listen` 的`onError`只记录日志且`onDone`重连回调被注释，客户端只能等待60秒心跳，未达到10秒恢复门槛。仓库内Apache-2.0源码副本仅恢复这两个终止事件到SDK已有的1.5秒重连回调，并用前后文件SHA-256锁定。重复停止/启动固定WuKong容器后，Android从启动恢复计时8.82秒收到新`ConnackPacket`；恢复后发送`Android_reconnect_ACK_2026-08-12`取得`SendAck reason=1`，业务历史回读WuKong消息ID `2087227292634746880`和频道序号3。

后续长时运行又发现上游`connect()`在替换旧Socket时，旧Socket的异步`onDone`会在新连接建立后继续排队重连；多个不可取消的延迟任务会让同一App device反复关闭并重新建立master连接。固定补丁现为每次连接分配单调递增代次、只保留一个延迟重连Timer，并丢弃已被替换的Socket和异步连接回调；应用网关也复用正在进行的连接Future。API 35模拟器清数据后重新登录Alice，冷启动15秒仅1次连接、1次CONNACK、0次远端关闭；重启固定WuKong容器后只在服务不可用期间按官方间隔重试，恢复后保持单连接，25秒内无额外连接或远端关闭，并发送`ReconnectPatchACK_20260812`取得`SendAck reason=1`。

同日对固定服务端源码的消息事件实现逐字段复核后，发现 Flutter SDK 1.7.9 能解析旧流字段但未暴露到消息对象，且会在协议包类型范围检查处拒绝 v2.2.5 的 EventPacket；JS SDK 1.3.5 已提供 eventManager，Easy SDK 1.0.3 已提供 customEvent。本地 Apache-2.0 最小补丁补齐 EventPacket 解码/分发和旧字段暴露，6个变更文件及对应上游文件均写入版本锁。再次与固定服务端依赖的 WuKongIMGoProto v1.2.3 逐字节对照后，确认帧类型依次为 DISCONNECT=9、SUB=10、SUBACK=11、EVENT=12，协议 v2–v4 的旧流接收字段严格为 `uint8 flag/string streamNo/uint64 streamId`，v5不再编码这些字段。早先使用错误 Flutter 枚举构造的合成 Event 10 测试因测试和实现共享同一个错误常量而失效；现有测试使用字面量 `12 << 4`，并另以字面量构造协议 v4 流锚点帧验证 SettingStream=2、旧流字段、64位消息标识、序号、时间和正文。业务服务测试验证锚点、delta、snapshot同步、所有权和非文本拒绝。合成帧证据仍不替代固定容器真实事件包和四端目标设备验收。

随后在固定本地镜像执行真实业务链路时，消息存储值证实 `SettingStream=2`；这与服务端依赖的 WuKongIMGoProto v1.2.3 源码一致，也确认固定服务端设计文档中示例值32是错误示例（32实际为Signal）。修正常量及单聊接收方事件同步的原始频道方向后，Bob向Alice创建流消息 `2087250409771929600`：两个delta保持open，close/finish分别持久化为事件序号1/2，finish键被服务端强制为`__finish__`；Alice通过业务eventsync取得两条事件，DataSource历史正文和`event_meta.events[main].snapshot`均为`Live stream complete`且`completed=true`。这证明服务端到业务DataSource的真实闭环。

修正帧常量和旧流字段布局后，把带真实服务地址的Debug APK重新安装到Android 15/API 35目标模拟器，并保持Alice的官方Flutter SDK TCP连接在线；Bob创建流消息 `2087259852957978624` 后连续发送两个delta、close和finish。Android在close前、无需刷新或历史回读时，会话预览已经即时合并为`REALTIME EVENT 12 PASS`，且占位正文消失；最终Logcat没有未知包类型、范围越界、解码失败或未处理异常。这是Flutter补丁实际解码WuKong `EventPacket(type=12)`并传递到页面状态的目标端证据，截图为`apps/mobile/build/qa/android-event12-final.png`，close前语义树为`apps/mobile/build/qa/android-event12-live.xml`，最终语义树为`apps/mobile/build/qa/android-event12-final.xml`；iOS和macOS目标端仍需分别完成同类实时验证。

同日新增`tools/wukong-web-probe.mjs`并在隔离的Headless Edge中加载官方JS SDK 1.3.5。探针真实创建两名业务用户、好友和单聊，以Web device flag完成WSS连接；断网前收到流锚点和`stream.delta`，CDP切断网络并关闭底层WebSocket后收到Disconnect，恢复网络后1.959秒再次Connack，并再次实时收到新流锚点和`stream.delta`。留存证据包含2条锚点和断网前后各1条delta事件，证明Web端`eventManager`和3秒重连路径在真实固定容器上闭环；单次浏览器结果仍不能替代95%客户端容量门槛。

同一模拟器安装加入输入状态处理的最新APK后，Alice停留在Bob单聊页；Bob通过受鉴权`/v2/channels/conversations/{id}/typing`提交`typing=true`，事件经业务Outbox和WuKong CMD实时到达，聊天标题副文案显示绿色`正在输入…`。提交`typing=false`后标题恢复`稍后回复`。Flutter发送侧只在输入框有焦点且正文非空时上报，3秒节流、4秒主动结束；接收侧使用本地6秒上限防止时钟偏差或异常载荷留下永久状态，收到该用户的新消息也立即清除。证据为`apps/mobile/build/qa/android-typing-live.png`、`apps/mobile/build/qa/android-typing-live.xml`和`apps/mobile/build/qa/android-typing-cleared.xml`。

同日另建流消息 `2087254227939135488` 验证snapshot路径：相同`eventId`重复提交两次都返回事件序号1，接收方无权追加返回403，带调用方snapshot的close返回400；合法close/finish取得序号2/3，最终DataSource正文与`event_meta`快照均为`SNAPSHOT EVENT PASS`且`completed=true`。固定源码同时表明锚点正文只是占位文案，不会自动进入事件快照；Flutter因此持久记录流正文是否已初始化，首个delta替换占位文案，后续delta追加，snapshot覆盖，并串行处理事件以避免高频包并发覆盖本地加密缓存。

混合事件链路曾暴露固定 WuKongIM v2.2.5 的服务端缺陷：`handleStreamPersist`持久化snapshot时未同步`MessageEventCache.TextSnapshot`，close又用旧内存delta覆盖数据库snapshot。现已在固定提交`a888f895`上加入最小可审计补丁：持久snapshot原子替换文字或非文字缓存，并以`HasTextSnapshot`区分“空文字快照”和“尚无快照”；后续delta只追加到新快照。Linux单测覆盖旧delta→snapshot→新delta→close、空快照和非文字替换；补丁容器真实回归的终态投影为`replacement after`，不再含旧`old`，因此混合snapshot流的该发布阻塞已解除。

同日执行真实离线补齐：强制停止Android App并确认进程消失后，Bob通过受鉴权业务发送接口发送`Bob_offline_to_Android_20260812_012612`；Alice仍离线时，PostgreSQL中的WuKong消息索引已经记录消息ID `2087229108403474432`、`client_msg_no=offline_android_20260812_012612`和频道序号4。随后冷启动Alice，6.14秒取得新`ConnackPacket`，会话DataSource同步后消息列表精确显示该正文。这证明单台Android目标模拟器的冷启动离线补齐链路，不能替代95%客户端恢复容量验收。

同日使用完整媒体鉴权链路验证GIF：Bob经`presign -> MinIO PUT -> checksum complete -> channel bind -> send`发送34字节`image/gif`，WuKong消息ID为`2087234606880165888`、频道序号5、内置正文类型3，正文只持久化`mediaId`和不可伪造的服务端元数据。Android首次收到后暴露出本地签名地址仍为`127.0.0.1:9000`的配置缺口；`compose.android-emulator.yaml`现同时覆盖WuKong TCP和MinIO客户端外部地址为`10.0.2.2`。服务重建后业务接口签发模拟器可达地址，Alice冷启动重新同步并在聊天气泡中完成GIF解码渲染，截图保存在`apps/mobile/build/qa/android-gif-fixed.png`。生产环境仍必须使用真实TLS对象存储域名，消息正文不保存短期签名URL。

2026-08-12后续真实Web拖放验证发现，同一开发服务若把全局`IM_S3_PUBLIC_ENDPOINT`设为Android模拟器专用`10.0.2.2`，浏览器会在MinIO PUT阶段失败。媒体服务现只在开发环境接受固定的`IM_S3_ANDROID_PUBLIC_ENDPOINT`，并从受限的`X-Client-Platform`上下文选择签名器；Web/macOS使用默认公网端点，Android使用模拟器端点，生产环境明确拒绝该覆盖。完整Edge UI探针经`presign -> PUT -> complete -> bind -> WuKong send -> SENDACK -> history`发送PNG，浏览器使用`127.0.0.1:9000`，消息ID为`2087355611204194304`、频道序号24，页面解码图片并由“发送中”收敛为“已发送”，浏览器无异常和网络失败；同一消息在Android业务历史中重新签名为`10.0.2.2:9000`并实际渲染为“已送达”。媒体发送遗漏的SENDACK跟踪已与文本发送统一，图片、GIF、语音、视频和文件共享同一状态收敛路径和回归测试。

Android反向发送也使用系统Photo Picker完成真实验证。首次选择时，`image_picker`的`imageQuality/maxWidth`参数把GIF正文转成JPEG却保留`.gif`名称；上传服务完成阶段检测真实魔数后返回400，错误媒体没有进入WuKong。相册路径改为不传转码参数、在读取原始字节后再分流静态图编辑与GIF直传。复测上传454324字节，服务端SHA-256为`bf119e9b35c04a5f13da2b6ac36d354f032468d355c62197b39c1f51cd1b20c6`，WuKong消息ID `2087240933329244160`、频道序号6、正文类型3、媒体ID `med_26f13944f22a723f0550e42f`；Android气泡实际播放并显示已送达，截图为`apps/mobile/build/qa/gif-send-final2.png`。

把上述跨端媒体与统一ACK修复重新构建为当前Android Debug APK并安装到API 35模拟器后，完整Flutter SDK仍连接`10.0.2.2:5100`并取得协议v4 Connack。重启固定WuKong容器后7.928秒恢复，稳定观察期仅1次Connack、0次恢复后远端关闭；恢复后发送`AndroidCurrentReconnectACK_20260812`取得`SendAck reason=1`，业务历史回读消息ID`2087357584888467456`、频道序号25。当前SDK初始化同时向消息管理器注册自定义正文`1001–1008`；CMD仍为99，实时事件包仍为12，不把业务正文类型注册和协议包类型混用。
- 好友接受事务产生 `friend.allow` Outbox；双向单聊白名单完成后消息发送成功，白名单完成前固定服务端精确返回 `ReasonNotInWhitelist`。
- 群创建事务产生 `channel.reconcile` Outbox；频道和订阅者同步完成后群消息 ACK、接收均通过。
- 固定版本 PDK 插件 `wk.plugin.im-policy` 以 `Send` 方法、版本 `1.0.0`、优先级 `1` 注册成功；真实 TCP 客户端的好友/群成员消息被允许，非群成员向同一群发送时由插件精确返回 `ReasonNotAllowSend` 且 `message_seq=0`。
- 接收端离线后，最近会话同步和 `/channel/messagesync` 均返回同一 `client_msg_no`，证明消息正文和频道序号来自 WuKongIM。
- `msg.notify`、`user.onlinestatus` 经固定源码的 gRPC Webhook 落入 PostgreSQL；重复事件唯一键测试通过。
- Server DataSource 的 `getSystemUIDs` 命令按固定源码的 JSON envelope 返回 200，内部入口拒绝公网源地址。

当前已证明固定协议的多设备同步/精确踢出、Web JS SDK WSS断网恢复与实时事件、Android目标模拟器重连/ACK和macOS Easy SDK WSS；尚未据此宣称完整四端目标设备互通或95%重连指标完成，仍须在iOS/macOS目标设备和正式容量环境验收。

固定 Server 源码的 `Datasource.GetChannelInfo` 当前构造了 `channelInfo` 却返回 `wkdb.EmptyChannelInfo`。因此 `channelInfoOn` 保持关闭，频道资料通过已认证内部 REST DataSource提供；自定义镜像不扩大补丁范围修改该上游路径。

固定 Server 和官方 SDK 对社区话题使用`communityID@topicID`，但上游通用特殊字符校验曾让`/channel`、`/channel/subscriber_add`和`/channel/whitelist_*`拒绝`@`。固定提交补丁现仅对频道类型5允许“左右非空、恰好一个`@`、两侧均不含`@/#/&`”的结构，其他频道类型的原校验保持不变。Linux API单测覆盖合法和非法边界；真实补丁容器已完成类型5创建、重置订阅者与白名单写入，三条Manager API均成功。Reconciler仍保留永久4xx延期和瞬时错误重试语义，避免未来配置冲突阻塞后续频道。

2026-08-12对固定源码日志路径复核时发现，上游连接对象字符串会输出会话AES Key/IV，认证失败和消息验签错误还会输出Token、签名及验签摘要；消息发送API、Token更新API和首帧错误路径还可能输出消息正文、订阅者列表、完整Token请求或原始协议帧。`v2.2.5-20260422-linli.3`补丁将这些值从源头移除，保留UID、消息编号、频道类型、正文大小、连接ID、设备标志、节点和协议版本等诊断字段；补丁SHA-256为`6163ffc38a5bf4fbed2d7c94610c336708066f05df5a2bb5f49e20951f16b01a`。静态门禁会拒绝完整发送请求、Token请求、订阅者列表和原始首帧日志。固定`linli.3`容器的Linux测试、amd64编译、类型5/流式快照回归与完整协议探针均通过；运行时门禁真实触发同一App主设备重复登录，旧连接收到`ReasonConnectKick`，原始容器日志保留`close old conn for master`且未出现Token/消息正文金丝雀或AES/签名校验字段。最终schema 44业务服务上的20连接、100 msg/s、200条消息策略冒烟得到200/200 ACK与投递、0拒绝/0未确认，ACK P95 27.50 ms、P99 38.18 ms，完成后Webhook pending/failed/正文残留均为0。

schema 44将Webhook收件箱改为提交前消费：`msg.notify`只保留消息ID、序号、类型、摘要和路由元数据，`msg.offline`只生成不含正文的每用户推送路由，`user.onlinestatus`按官方`uid-deviceFlag-online-connId-deviceOnlineCount-totalOnlineCount`格式更新在线投影，`msg.stream`正文仍只归WuKong事件存储。每种事件完成后把收件箱`payload`置为`{}`；启动恢复使用保存点逐条隔离旧脏事件。2026-08-12本地迁移实际消除8,656条旧积压，迁移后及新事件运行后均为0条pending/processing、0条已完成或失败事件正文残留。

## 必须暂停确认的门槛

- 固定源码、发布 SDK 与官方文档出现无法由测试解释的字段/行为冲突。
- 某端无法达到相同业务语义且需要改变用户可见行为。
- 需要新增收费服务、购买带宽/磁盘或改变当前单机拓扑。
- 任何正式数据库、WuKong数据卷、旧消息卷清空。
- 正式上线前服务器数据盘少于 1 TB，或群视频/消息压测未达到计划门槛。
