# 企业通话握手不确定状态：冷维护修复

本工具处理“LiveKit 可能已接受连接，但企业代理未收到 HTTP 101 确认”的故障。
不确定记录不能按超时删除，否则撤权可能漏掉迟到的通话连接。正常发送／聊天无需运行此工具。
当前提供的是**运维确认后的冷修复**，不是自动重试、后台任意 Shell 或用户自助解除限制。

## 前置边界

- 仅支持现有受控代理登记的九服务企业栈、schema 79 和完整原部署 journal。
  平台仍管理归属和企业停用；企业媒体握手记录不能授予新账号权限。
- 运维先通过既有平台企业停用流程关闭业务访问。即使握手阻断导致停用任务尚未完成，
  企业库也必须已经在指定 `accessVersion` 保存 `access_enabled=false`，且存在对应的
  **关闭**操作。`resuming`、其他版本／操作 ID、尚未关闭访问均不能修复。
- 在明确维护窗口停止本企业部署代理，使用其**原状态目录**。工具取得相同 OS journal
  排他锁；代理仍运行、正在部署或有未完备份任务时拒绝执行，不抢锁、不创建替代 journal。
- 停止本企业原网关、API、IM、LiveKit、媒体初始化、MinIO、Redis 和插件服务；只保留
  PostgreSQL 运行。此步骤会中断本企业所有现有连接，不允许在未批准的生产维护窗口执行。
  只停止从原发布、项目标签及具体容器 ID 核对出的本企业服务；不能批量停止宿主机所有容器。
- 宿主机监督进程、其他运维和平台调度不得在窗口内重启写入者。工具检查原容器、固定镜像、
  发布摘要、部署代次、实际挂载及六个原卷身份，数据库不得有其他客户端连接。
  未受控旧部署应先完成 R3 的接管，不得拿新 journal 冒充已登记企业。

## 命令与私有配置

运维使用当前版本 `server/cmd/tenant-backup` 构建的本机程序。不是 App 安装包，也不是
企业 HTTP 接口；无需备份加密密钥。配置与其他私有运维文件放在受限目录，不提交 Git。
以下为**企业 Linux 主机**的路径结构示例，全部占位值必须由原部署记录替换：

```json
{
  "expected": {
    "tenantId": "tenant-a", "serverId": "server-a",
    "releaseId": "CURRENT_RELEASE_ID", "releaseDigest": "CURRENT_RELEASE_DIGEST",
    "generation": 1, "accessVersion": 2, "schemaVersion": 79
  },
  "hostFingerprint": "REGISTERED_HOST_FINGERPRINT",
  "runtime": "linux/amd64",
  "stateDirectory": "/private-agent/state",
  "bundleDirectory": "/private-agent/releases",
  "dockerBinary": "/usr/bin/docker",
  "dockerEndpoint": "unix:///var/run/docker.sock",
  "catalog": ["REPLACE_WITH_ORIGINAL_VERIFIED_RELEASE_OBJECTS"],
  "mediaRepair": {
    "requestId": "media-repair-unique-id",
    "pauseOperationId": "EXISTING_PLATFORM_PAUSE_OPERATION_ID",
    "actor": "operator-id", "reason": "已确认停止原信令服务，处理握手确认丢失",
    "confirmed": true
  }
}
```

不填 `archiveDirectory`、`restoreReleaseId`、`restoreSequence` 或 `-key-file`，也不能混用
备份／恢复操作参数。`catalog` 必须是真实 `Release` 对象数组，不是上述说明字符串。
在确认主机和窗口后执行程序参数：

```text
tenant-backup -mode repair-media -config /private-operator/media-repair.json -confirmed
```

工具本身**不会 stop/start/up/pull/remove 服务或卷**。它只在已经停写且绑定正确时，通过
原 PostgreSQL 容器执行固定事务：锁定企业及握手记录，确认访问关闭／停用操作／schema／
无其他客户端，清除不确定握手阻断，并同事务写 `tenant.media.cold_repaired` 审计。
审计含操作者、理由、绑定、原运行资源证明摘要及清理数量，不复制媒体令牌、会话或消息正文。
输入文本以编码数据传入，不插入 Shell／SQL 语法，数据库错误正文不输出。

结果只返回请求 ID、企业、访问版本、原资源证明、清理数量及 `accessEnabled:false`。
只有原输入、原部署和原资源完全相同的重复执行才返回原结果，不重复清理或写审计。
错误输出、断电或确认丢失时保持停写，保留配置和 journal，核查后重试原请求；不要换新 ID
反复删除，也不要手工标记撤权任务完成。审计失败整笔回滚，阻断记录仍在。

## 修复后

1. 成功修复仍不代表企业撤权完成，业务访问继续关闭；工具不更改群、账号、会话、权限版本
   或平台任务。按原部署依赖顺序只启动已核对的原容器 ID，不用 `compose up` 创建新空卷。
2. 验证服务健康后恢复原代理／平台任务。让原停用或账号撤权任务继续执行正常 IM 断开及
   LiveKit 参与者检查；只有这些检查完成，平台才可确认原任务结束。
3. 若要重新开放企业，另走原有确认后的平台启用流程（新访问版本）。旧 API／IM／通话
   凭据不能因这次清理复活；企业调换的源身份仍须确认撤权后才能启用目标身份。

若容器或卷丢失、部署绑定变更、schema 不符、停用版本变化，工具拒绝继续，不自动修复环境。
保留数据，按对应恢复／重新校对流程处理。不要因修复失败转为按时间批量删除握手记录。

## 验证入口

PowerShell 7，在仓库根目录使用已经构建的固定本机测试镜像：

```powershell
./infra/scripts/build-enterprise-bundle.ps1
./infra/scripts/test-tenancy.ps1 -StartDependencies -BackupDocker
```

脚本编译真实运维 CLI，在随机独立企业栈注入未确认握手，覆盖运行态拒绝、错误停用 ID、
代理排他锁、审计失败回滚、重复执行、原服务恢复与正常撤权继续。测试数据和容器精确清理，
持久默认企业不能变化。故障记录为注入模型；实际 PostgreSQL／Docker／IM／LiveKit 及 CLI
链路真实运行，不宣称测试了云主机断电、生产维护窗口或真实移动端通话质量。
