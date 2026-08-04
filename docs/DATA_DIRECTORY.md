# 服务器统一数据目录

服务器的统一管理入口是 `/data/linli-im/`。这个目录同时作为宝塔文件管理器中的首选入口。

```text
/data/linli-im/
├── README.md                 本说明的服务器副本
├── application/
│   ├── current -> ...           当前运行版本
│   └── releases -> ...          历史发布版本，用于回滚
├── compose -> ...             Docker Compose、Caddy 和监控配置
├── shared/                    集中配置、IP 证书、只读密钥和下载包
├── backups/                   PostgreSQL 与 MinIO 备份
├── logs/
│   ├── archive               人工或发布前导出的分服务日志快照
│   └── incidents             故障调查证据，按事件单独建目录
└── data/
    ├── postgres/data          PostgreSQL 主数据
    ├── postgres/archive       PostgreSQL 归档目录
    ├── redis                  Redis AOF 离线同步与临时状态
    ├── minio                  头像、图片、语音、视频和文件
    ├── caddy                 HTTPS 网关状态
    └── monitoring            Prometheus 与 Grafana 数据
```

## 管理规则

1. `shared/config.env` 是集中配置，权限必须为 `600`；不得从宝塔、群聊或工单中发送其原文。
2. 不要在容器运行时手工修改 `data/postgres`、`data/redis` 或 `data/minio`中的文件。
3. 修改 Compose 或配置后使用 `linli-im deploy`；日常巡检使用 `linli-im status` 和 `linli-im smoke`。
4. 备份必须通过 `linli-im backup` 生成，不得仅复制正在运行的数据文件。
5. 旧 Docker named volume 在首次迁移后保留为应急回退副本；验收和备份周期完成前不删除。
6. 日常日志继续由 Docker/宝塔按容器查看并自动轮转；需要长期保留时执行 `linli-im logs-export 24h`，归档会按服务拆分并写入校验文件。
