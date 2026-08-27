# 青蛙呱呱管理后台设计 QA

## 对比基准

- source visual truth：`https://react-demo.tailadmin.com/`
- source capture：`artifacts/design-qa/tailadmin-reference-740.jpg`
- implementation：`http://127.0.0.1:7361/overview`
- implementation capture：`artifacts/design-qa/qingwa-overview-740.jpg`
- navigation capture：`artifacts/design-qa/qingwa-navigation-740.jpg`
- secondary real-data capture：`artifacts/design-qa/qingwa-online-740.jpg`
- viewport：740 × 803 CSS px
- source pixels：740 × 803
- implementation pixels：740 × 803
- devicePixelRatio：2；浏览器截图工具已归一化为每个 CSS px 对应一个输出像素，不再二次缩放
- state：浅色主题、已登录、真实远程数据、响应式平板宽度；概览与移动导航分别检查

## 全屏对比证据

TailAdmin 参考和青蛙呱呱实现使用相同视口并在同一次浏览器比较中查看。实现保留了参考的灰白画布、细边框白色卡片、Outfit 字体、两列指标布局、宽图表面板和低阴影层级；参考中的品牌蓝仅在相同语义位置替换为青蛙绿。指标、图表和审计内容来自远程服务，因此不复制 TailAdmin 的电商演示数据。

## 聚焦区域对比

移动导航另以 `qingwa-navigation-740.jpg` 聚焦检查。菜单抽屉位于 64px 顶部栏下方，Logo、功能分组、选中态、账户动作均完整可见；背景遮罩没有覆盖顶部栏或截断侧栏。登录页另与 TailAdmin `/signin` 在 1280 × 720 视口中并排检查：保持官方左右分栏、表单宽度和纵向节奏，但删除了服务端不支持的 Google/X 登录、注册、忘记密码和营销说明。

## 必查保真面

- 字体与排版：本地打包 Outfit 400/500/600/700；中文回退 PingFang SC、Microsoft YaHei；标题、指标、正文和表格形成稳定层级，无跨页字体漂移。
- 间距与布局：桌面侧栏 290px / 90px、桌面内容最大 1536px、卡片 16px 圆角、24px 主栅格；740px 下为两列指标和抽屉导航，无页面级横向溢出。
- 颜色与令牌：TailAdmin gray-50/100/200/500/600/900 灰阶保持一致；brand-blue 位置映射为 brand-green；警告、错误、信息色仍独立。
- 图片与资源：使用项目真实 `qingwaguagua-mark.png`，没有用 Emoji、CSS 图形或手绘 SVG 代替 Logo；Lucide 图标保持统一线性风格。
- 文案与内容：固定文案均为后台真实功能；所有指标、表格和状态来自服务端；没有演示开关、占位业务或虚构通知。
- 交互与无障碍：侧栏展开/收起、移动抽屉、分组折叠、命令搜索、真实路由跳转、焦点返回、Escape 和未保存确认均有测试；焦点可见，状态不只依赖颜色。
- 控制台：概览、在线状态、导航抽屉和登录页检查时均为 0 条控制台错误。

## Findings

- [P3] 平板顶部栏保留“实时数据”而不是 TailAdmin 的省略号菜单。
  - 位置：`topbar-actions`
  - 证据：参考在该宽度显示应用菜单；实现显示真实服务连接状态。
  - 影响：属于业务信息差异，不改变结构、密度或核心使用。
  - 处理：保留。该状态对 IM 运营比模板应用菜单更有价值，且没有加入虚构通知。

## 比较历史

### 第 1 轮

- [P2] 移动侧栏从页面顶部开始，Logo 被 64px 顶部栏遮挡。
- [P2] 740px 宽度仍显示桌面命令搜索，和 TailAdmin 的移动头部结构不一致。

已修复：

- 在 1120px 以下让侧栏从顶部栏下方开始，并按 74px / 64px 两个响应式高度计算可用视口。
- 在 1024px 以下隐藏桌面命令搜索与账户详情，显示真实青蛙 Logo 和产品名。

### 第 2 轮

后续证据 `qingwa-overview-740.jpg` 与 `qingwa-navigation-740.jpg` 显示 Logo 不再裁切、顶部栏无拥挤、抽屉可完整扫描；未发现新的 P0/P1/P2 问题。

## 实施检查清单

- [x] TailAdmin Free React 结构与灰阶
- [x] 蓝色品牌位映射为青蛙绿
- [x] 青蛙 Logo 与产品名称
- [x] 真实业务菜单重新分组
- [x] 真实远程 API 与鉴权边界
- [x] 桌面、平板、移动响应式
- [x] 109 项自动化测试
- [x] TypeScript 检查与生产构建
- [x] 浏览器控制台与容器健康检查

final result: passed
