# 命名与兼容标识

## 统一规则

- 对外产品名称：**青蛙呱呱**。
- 新增服务、镜像、任务和监控资源建议使用：`linli-im`。
- Go 服务健康标识和模块边界使用：`linli-im` / `github.com/linli/im/server`。

## 必须保留的旧标识

以下 `nexachat` 标识不是当前品牌，而是已经参与数据寻址、升级兼容或服务器自动化的技术标识。未经迁移方案不得重命名。

| 标识 | 保留原因 | 修改风险 |
|---|---|---|
| Compose project：`nexachat*` | 决定 volume、network 和容器命名空间 | 启动一套空 volume，看起来像数据丢失 |
| PostgreSQL 数据库/用户：`nexachat` | 已写入连接串和持久化卷 | 服务无法连接或误建空库 |
| 存储桶：`nexachat-media` | 历史对象键和公开媒体路径依赖 | 旧媒体不可访问 |
| `/opt/nexachat/*` | IP 环境发布、配置、证书和备份路径 | systemd、脚本和软链接失效 |
| `/usr/local/bin/nexachat` | 已安装的运维命令入口 | 定时任务和人工手册失效 |
| `NEXACHAT_ROOT` / `NEXACHAT_CONFIG` | 旧服务器脚本环境变量 | 自动化找不到发布与配置 |
| systemd、Prometheus 旧文件/规则名 | 已部署单元、告警路由和仪表盘引用 | 定时任务或告警静默失效 |
| Flutter package `nexachat` | Dart import、工程文件和平台构建引用 | 大范围源码及签名工程变更 |
| `nexachat.*` 客户端存储键 | 本地加密缓存和会话升级兼容 | 用户升级后被登出或本地数据不可读 |
| Admin `nexachat_*` 浏览器存储键 | 已登录会话和数据源偏好兼容 | 后台升级后会话丢失 |

仓库提供 `infra/scripts/linli-im-ops.sh` 作为新技术命名入口，但它只委托兼容脚本，不改变远端路径、systemd 或已安装命令。是否在服务器安装 `linli-im` 别名应作为独立、可回滚的运维变更处理。

## 未来迁移要求

确需替换兼容标识时，必须单独立项，并同时提供：数据备份、双读/双写或复制步骤、旧版本回滚路径、客户端升级策略、监控与 systemd 切换、验证脚本及负责人签字。禁止在普通 UI、文档或代码清理中顺手修改。
