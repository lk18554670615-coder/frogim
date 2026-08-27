# 本地前端连接远程资源测试

此模式用于在本机验证最新 Flutter 与管理后台界面，但所有业务数据和基础设施均来自远程服务器：Go API、WuKong WSS、PostgreSQL、Redis、MinIO、LiveKit 和推送服务都不会在本模式下本地启动。

## 首次配置

```bash
cp .env.remote-test.example .env.remote-test
make remote-test-validate
```

所有可修改地址集中在 `.env.remote-test`，并带中文注释。该文件已加入 `.gitignore`。严禁把管理员密码、Token、个推密钥或对象存储密钥写入任何前端环境变量。

## Flutter Web 与管理后台

```bash
make remote-test-up
```

- Flutter Web：`http://127.0.0.1:8093`
- 管理后台：`http://127.0.0.1:8088`

两个本地 Nginx 仅承载静态前端，并通过只允许业务路径的本地 TLS 网关，把 `/v2/`、`/api/`、WuKong `/im` WSS 与 LiveKit 信令安全转发到 `REMOTE_SERVER_ORIGIN`。网关严格验证公网证书，并保持远端允许的 Origin，不需要放宽生产白名单。

```bash
make remote-test-status
make remote-test-logs
make remote-test-down
```

## Android、iOS、macOS 真机或模拟器

```bash
make mobile-remote
# 指定设备示例
REMOTE_TEST_ENV=.env.remote-test infra/scripts/run-mobile-remote.sh -d <device-id>
```

原生客户端会使用 `REMOTE_API_BASE_URL` 登录，再按 `ImSession` 直连 WuKong TCP；Web/macOS 使用 WSS。媒体上传 URL 由远程 API 签发，因此实际文件进入远程 MinIO；音视频使用 API 签发的短期 LiveKit Token，禁止在客户端写死 API Secret。

## 防误连检查

`infra/compose.remote-test.yaml` 只含 `web`、`admin` 和无状态 TLS 转发网关，不包含数据库、Redis、MinIO、TURN 或 Go 服务。`make remote-test-up` 会先停止本地整栈的同名 Web/管理端容器，防止端口冲突，但不会删除任何本地数据卷。
