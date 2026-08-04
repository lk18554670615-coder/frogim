# 测试指南

所有命令默认从仓库根目录执行。测试分为快速检查、真实依赖集成、客户端构建和部署冒烟四层。

## 快速检查

```bash
(cd server && go test ./... && go vet ./...)
(cd apps/admin && npm ci && npm run lint && npm test -- --run && npm run build)
(cd apps/mobile && flutter pub get && flutter analyze && flutter test)
infra/scripts/check-docs.sh
bash -n infra/scripts/*.sh
```

服务端并发和数据竞争检查：

```bash
(cd server && go test -race ./...)
```

## 真实 PostgreSQL 集成

先启动本地依赖，再显式提供测试连接串：

```bash
docker compose -f infra/compose.yaml up -d postgres redis minio
(
  cd server
  IM_TEST_DATABASE_URL='postgres://nexachat:nexachat@127.0.0.1:5432/nexachat?sslmode=disable' \
    go test ./internal/store -count=1
)
```

测试使用随机 ID，并清理自己的数据。不得把生产连接串传给 `IM_TEST_DATABASE_URL`。

## 本地整栈冒烟

```bash
cp .env.example .env
docker compose -f infra/compose.yaml up -d --build
infra/scripts/smoke-local.sh
docker compose -f infra/compose.yaml ps
```

通过标准：API `/ready`、后台 `/healthz` 正常；后台安全响应头存在；未登录管理接口返回 401；所有依赖容器健康。

## 本地真实产品链路验收

整栈启动后执行：

```bash
make acceptance-local
```

脚本通过真实 HTTP、PostgreSQL、Redis 和 MinIO 链路验证注册、资料、搜索、好友申请、单聊、消息幂等、回复、已读、撤回、群聊、群公告、黑名单、举报、反馈和账号注销。临时账号在成功结束时注销；该脚本只允许指向本地开发环境，严禁传入生产地址。

## Flutter 构建与设备验收

```bash
(cd apps/mobile && flutter build ios --simulator --no-codesign)
(cd apps/mobile && flutter build apk --debug)
```

真机至少覆盖：注册/登录、单聊、群聊、离线同步、撤回、转发、图片/语音/视频/文件、好友与屏蔽、公告、通知点击、前后台切换、弱网恢复、权限拒绝、深浅色、文字缩放和小屏布局。APNs、Android 厂商离线通道、短信和 TURN 必须在生产同配置的外部服务上验收。

## 生产配置静态验证

```bash
make production-validate PROD_ENV=.env.production
make production-config PROD_ENV=.env.production
```

验证配置不会部署。真实部署和远端冒烟由发布负责人按 [RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md) 执行。

## 证据留存

每次候选版本保存：提交号、测试时间、命令输出、失败重跑说明、容器镜像 ID、迁移版本、真机型号/系统、外部推送与短信证据、备份校验值和审批人。日志与截图必须脱敏。
