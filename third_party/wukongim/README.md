# 固定上游依赖

`versions.lock.json` 是 WuKongIM 迁移的上游事实基线。服务端标签对象与实际代码提交均被记录；包管理器产物使用发布归档的 SHA-256，而不是不稳定的仓库默认分支。

下载并校验全部源码归档：

```powershell
./third_party/wukongim/fetch.ps1
```

归档默认进入未纳入 Git 的 `cache/`。WuKongIM 的精确 OpenAPI 位于固定服务端归档的 `docs/api/openapi.json`，开发或审核时必须从该归档读取。不得用在线 `latest` 文档覆盖本锁文件。

`flutter-sdk-1.7.9-patched/` 是纳入 Git 的官方 1.7.9 最小补丁副本。它保留上游许可证、版本、数据库结构和既有公开 API，仅修复 Socket `onError/onDone` 重连回调、补齐固定服务端 v2.2.5 已定义但该 SDK 未处理的 EventPacket（协议帧类型 12；10/11 是 SUB/SUBACK），并按固定 GoProto 修正 Stream 位及 v4 旧流字段顺序后暴露该元数据。补丁不包含 AI Agent。补丁说明、原始归档哈希和所有变更文件前后哈希分别记录在该目录的 `LINLI_PATCHES.md` 与 `versions.lock.json`；Flutter 锁文件必须保持 `source: path`，禁止静默退回 Hosted 包。

校验本地补丁未漂移：

```bash
./infra/scripts/verify-wukong-flutter-patch.sh
```

生产 Compose 使用 `linux/amd64` 平台清单摘要。升级任何条目时必须同时更新版本、源码提交、归档校验和镜像摘要，并重新运行协议探针与四端回归。
