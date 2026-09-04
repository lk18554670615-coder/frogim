# 账号注册邀请码验收记录（2026-09-04）

## 实现范围

- 数据库版本升级至 63，为所有未注销历史用户幂等生成一个当前邀请码。
- 支持 `disabled`、`optional`（默认）、`required` 三种注册策略。
- 密码注册与验证码首次开户在同一事务内完成邀请码锁定、账号创建、个人码生成、邀请关系和业务审计。
- 已有账号验证码登录不要求邀请码；后台单个和批量开户豁免邀请码，但会生成个人邀请码。
- 用户可查看、复制、展示二维码并自行修改一次；历史代码永久保留且不得复用。
- 后台可查询邀请码和邀请关系，并按理由、确认和 `users.write` 权限执行启用、停用、重置。

## 自动验证

- `server`: `go test ./...`
- `apps/admin`: `npm test`，`npm run build`
- `apps/mobile`: `fvm flutter analyze --no-pub`
- `apps/mobile`: `fvm flutter test --no-pub test/auth_policy_test.dart test/auth_flow_edge_cases_test.dart test/auth_screens_test.dart test/settings_screens_test.dart`

PostgreSQL 生命周期测试位于 `server/internal/store/invitations_integration_test.go`。该测试使用独立 schema，覆盖 62→63 迁移、重复迁移、历史用户补码、大小写校验、绑定关系、自主修改、启停、重置和额度保持；需要设置 `IM_TEST_DATABASE_URL` 才会执行，未配置时明确跳过，不会连接开发或生产数据库。

## 发布检查

1. 备份 PostgreSQL，并先发布数据库／服务端。
2. 服务启动后确认 `im_schema_migrations` 最大版本为 63，且每个未注销用户恰好有一个 `active` 或 `disabled` 邀请码。
3. 发布管理后台，保持策略为默认 `optional`，验收邀请码查询、关系查询、停用、启用和重置审计。
4. 发布 Flutter 客户端，验收手填、粘贴、扫码、复制和一次自主修改。
5. 仅在覆盖旧客户端的强制更新生效后切换为 `required`；否则旧客户端仍可登录已有账号，但无法创建必须填写邀请码的新账号。

邀请码校验接口只返回布尔结果，不返回邀请人昵称、头像、手机号或用户 ID。邀请码不会自动加好友，也不参与群邀请。
