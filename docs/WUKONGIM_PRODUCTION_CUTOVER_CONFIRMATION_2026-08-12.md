# WuKongIM 正式切换确认单（2026-08-12）

> 最新复核（2026-08-13）：服务器源码指针已更新为提交 `7e68f33e5c1dcbce3626582b5dc4869341033fa2`；Go 与 Flutter Web 运行提交 `59b49d25358a64f2ac6b73a2272741720c6c61e6` 镜像，管理后台继续运行已验收的 `08d160dfd6553a6bec6d8858e33629ed2fe7f6c2` 镜像，全部容器健康。Web Push 的 VAPID、耐久投递和 Service Worker 已上线，但普通浏览器尚未由用户手势授予通知权限，设备表也尚无原生推送注册。运行栈仍按开发阶段要求使用 `IM_PUSH_PROVIDER=log`；服务器配置没有个推或 APNs VoIP 凭据。`/legal/terms` 与 `/legal/privacy` 均返回 200，但正文明确限定为开发测试说明，不能代替运营主体/法务正式文本。异地备份实现和恢复门禁已完成，实际 `BACKUP_OFFSITE_ENABLED=false`，仍需外部 S3 目标。当前 Windows 环境没有 Xcode、iOS/macOS 目标设备或 Android 实体真机，因此这些目标端与杀进程推送验收不能由本机自动化代替。用户已明确暂缓 1 TiB 磁盘门槛、正式数据清空确认和额外安全加固，这三项不再作为本开发阶段替换的阻断项。

> 执行状态（2026-08-12 18:30 CST）：用户已明确授权开发阶段完全替换旧项目，本确认单中的旧栈清理与新栈部署已经执行。服务器源码发布目录已更新到提交 `908f5d6`，运行时另已应用并验证 `30e8b0d` 的旧 `/v1/*` 边缘404规则；Compose 项目仍名为 `nexachat-ip`（仅为兼容既有运维路径），但运行内容已经全部替换为 WuKongIM、LiveKit、Go 业务服务、Flutter Web、React 后台、PostgreSQL、Redis、MinIO、Prometheus 和网关。旧 Coturn、旧容器、旧镜像、旧发布目录和旧运行数据目录均已删除，不再存在双栈或回退流量。
>
> 最终旧栈备份位于 `/data/linli-im/backups/pre-wukong-cutover-20260812T072345Z`，SHA-256 全量校验通过，隔离恢复与源数据计数一致。新栈备份位于 `/data/linli-im/backups/20260812T080118Z`，隔离恢复得到 63 张表、7 张关键表、schema 45、342 项约束和 155 个 WuKong 文件。以下“切换前事实”与操作清单保留作审计记录，不再描述当前运行状态。

本文只记录已通过只读检查确认的事实。执行本单中的破坏性步骤前，必须由项目负责人明确确认磁盘方案、维护窗口、旧客户端停用时间和数据清空范围。

## 目标环境

- 服务器：`119.28.190.45`（主机名 `VM-0-16-tencentos`）
- SSH：已验证 `root` 免密连接可用
- 当前 Compose 项目：`nexachat-ip`
- 当前发布目录：`/opt/nexachat-next-908f5d6f2ad5275c489e66a689cc1ed1b2a98fa2`
- 当前链接：`/opt/nexachat/current` → 上述发布目录；该目录已同步 `30e8b0d` 的网关规则，下一次原子发布会以完整提交目录替换该运行时热修
- 当前 Compose 配置：`/opt/nexachat/current/infra/compose.ip.yaml`
- 当前环境文件：`/data/linli-im/shared/config.env`

## 当前运行事实

- 旧服务共8个：gateway、web、server、admin、postgres、redis、minio、coturn。
- 当前没有 WuKongIM、LiveKit、Prometheus 和新版备份指标服务。
- 根盘：200 GB，总使用约16 GB、可用约184 GB；低于计划规定的1 TB上线门槛。
- 内存：15 GiB，检查时约1.9 GiB已用、13 GiB可用。
- 当前旧 Coturn仍监听3478；新链路验收通过后才删除。

## 现有数据与备份

现有持久数据目录：

- PostgreSQL：`/data/linli-im/data/postgres/data`
- PostgreSQL归档：`/data/linli-im/data/postgres/archive`
- MinIO：`/data/linli-im/data/minio`
- Redis：`/data/linli-im/data/redis`
- Caddy：`/data/linli-im/data/caddy`
- 历史备份：`/data/linli-im/backups`

最近已发布旧栈备份：`/data/linli-im/backups/20260811T205232Z`。

- `SHA256SUMS`中的 PostgreSQL和2个 MinIO对象全部校验通过。
- PostgreSQL快照已在临时 PostgreSQL 17容器中真实隔离恢复：35张公共表、24个用户、schema 25、210项约束。
- 这是旧栈备份，不含 WuKongIM目录；正式切换前必须再生成最终旧栈快照。

本地新栈 schema 45快照也已真实隔离恢复：64张公共表、7张关键表（包含`im_wukong_system_users`）、342项约束。

## 正式切换将清空或替换的对象

确认后才执行：

1. 停止 `nexachat-ip`旧 server、web、admin和coturn；冻结旧客户端写入。
2. 对当前 PostgreSQL、MinIO和配置生成最终旧栈快照并完成校验与隔离恢复。
3. 清空并重建业务 PostgreSQL数据基线；不迁移旧账号、好友、群和消息。
4. 创建全新 WuKongIM数据目录：
   - `/data/linli-im/data/wukongim/data`
   - `/data/linli-im/data/wukongim/logs`
   - `/data/linli-im/data/wukongim/plugins`
5. 旧 Redis仅作为缓存处理，不作为消息恢复来源。
6. MinIO旧对象保留在最终备份中；新数据库不引用旧对象。稳定观察期结束前不物理删除备份。
7. 启动 WuKongIM、LiveKit、Go业务服务、Flutter Web、管理后台和监控。
8. 完成冒烟后关闭并删除旧 Coturn服务；旧发布目录与最终备份在稳定观察一周后再决定是否删除。

不会在切换时删除：

- `/data/linli-im/backups`中的已发布备份；
- `/opt/nexachat/releases`中的旧发布目录；
- `/data/linli-im/shared`中的证书、下载文件和生产配置，除非先生成替代文件并复验。

## 新生产配置缺口

远端现有环境文件尚未包含下列上线必需类别：

- WuKongIM镜像摘要、Manager Token、用户Token密钥、策略密钥、TCP/WSS外部地址；
- LiveKit API Key、Secret、外部WSS地址；
- WuKong插件信任公钥和白名单；
- 正式用户协议、隐私政策URL；
- 新版备份指标目录和异地备份选择；
- APNs VoIP凭据（iOS真机来电需要）；Getui变量已存在但仍需真机到达验证。

切换时可自动生成服务内部随机密钥，但正式法律URL、APNs资料、异地备份目标不能由代码推断。

## 已通过的本地门禁

- Go全包测试通过。
- Flutter静态分析无问题，178项测试全部通过。
- 2026-08-13已生成并备份到本机受忽略目录的Android发布密钥，正式APK/AAB使用同一4096位RSA证书签名，APK v2/v3校验通过；双API 35模拟器已安装当前正式APK并完成消息、语音、屏幕共享、断网恢复和锁屏来电验收。商店上传前仍须由发布所有者确认密钥归属并完成安全异地备份。
- React后台31项测试、TypeScript检查和生产构建通过。
- 本地真实全链路产品验收通过，包含系统账号双向策略语义及WuKong运行缓存同步。
- 固定版本 WuKongIM、Android、Web、macOS Easy SDK协议探针已通过。
- 本地1000连接/1000 msg/s及5000连接/500 msg/s测试通过；目标机10k/1k正式容量测试尚未执行。
- LiveKit 10房间×9人真实媒体负载已在本机通过。

## 尚需明确确认

1. 磁盘方案：扩容到至少1 TB，或明确书面豁免计划中的1 TB门槛并接受当前单盘200 GB、非高可用上线。
2. 维护窗口：开始时间和允许中断时长。
3. 旧客户端停用时间：切换后立即拒绝旧版本登录。
4. 数据范围：确认清空旧 PostgreSQL业务数据并不迁移旧账号/好友/群/消息。
5. Apple条件：提供 iOS真机与 macOS设备/构建环境；缺少时不能宣称四端正式验收完成。
6. 异地备份：提供目标，或明确首版只保留本机备份。

只有上述项目得到明确回复后，才执行正式生产切换。

## 只读自动预检

在每次准备切换前执行以下命令；它只通过 SSH 读取主机、容器、配置键名和备份，不复制文件、不改配置、不启停服务：

```bash
make production-cutover-preflight HOST=119.28.190.45
```

预检会把生产配置、新版发布文件、24小时内可读备份、当前核心服务和10k/1k正式性能证据列为硬门禁；1 TiB磁盘默认仍是硬门禁，只有部署所有者明确接受容量风险并设置`WUKONG_REQUIRE_1TIB_DISK=false`时才降为警告。旧Coturn仍运行、WuKongIM/LiveKit尚未启动等切换前预期状态只显示警告。输出只列缺失键名，不输出任何密钥值。
