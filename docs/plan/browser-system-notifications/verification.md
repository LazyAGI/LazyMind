# 实现与验证结果（2026-09-17）

已实现浏览器系统通知：网页打开时接收、后台/最小化不主动停轮询、同源同用户多标签页协调、显式权限授权、任务结果标题/摘要、点击验证并定位结果。原 Electron 接收器不变；无新增生产依赖、服务、数据库迁移或外部渠道改动。

## 自动化验证

- 文件 SQLite：`cd backend/core && go test -race . ./taskcenter ./scheduler -run TestNotification -count=1`，三包通过。
- PostgreSQL：同一命令，设置 `TEST_DB_DRIVER=postgres` 和独立测试库的 `TEST_DB_DSN`，三包通过。复用本机通知测试数据库容器，每个测试独立 schema，未操作业务库。
- 前端：Node 24 下执行 `pnpm exec vitest run src/modules/notifications src/runtime/desktopBridge.test.ts src/runtime/managedBrowser.test.ts`，7 个测试文件、51 项测试通过。
- Electron：`cd desktop && node --test scripts/native-notifications*.test.mjs`，80 项通过。
- 真实 Chromium：`frontend/scripts/browser-notifications.e2e.mjs` 在独立临时浏览器配置、回环测试 HTTP 服务上运行。生产浏览器消费者、真实 Web Locks、站点存储和 Notification 被执行；认证/网络响应为测试 Fixture。两标签页提交 1 次、浏览器 show 回执 1 次、标签页接管重复 0 次；点击实际跳转到预期会话路径。未使用用户浏览器配置或真实任务/平台凭据。
- 受影响前端 ESLint、`git diff --check`、发布构建通过；OpenAPI 的两个接收接口、依赖 schema 和生成客户端同步，其余既有接口内容保留。
- 定向 TypeScript 检查：本次修改的生产/测试文件诊断 0。全仓 `tsc --noEmit` 仍因既有 `frontend/src/modules/chat/utils/message.test.ts:49` 两处 TS1128 失败，未修改该无关文件，不声明全仓类型检查通过。

日志：`/tmp/lazymind-browser-{core-sqlite,core-postgres,frontend-final,electron,e2e,lint,build,types,types-full,openapi-client}.log`。

初次验证遇到的环境问题已处理：系统默认 Node 20 无法运行当前 jsdom/undici，改用已安装 Node 24；Java 未在默认 PATH，改用现有 Homebrew OpenJDK；PostgreSQL 测试辅助程序要求 URI DSN，改用 URI 后通过。没有为这些问题修改生产依赖。OpenAPI 导出对容器专用 `/openapi-export` 的写入在 macOS 只读根目录失败，但仓库内契约已成功导出，生成及检查成功。

## Safari Local 联调修复与验证

真实 Local 接口联调发现：通知 ORM 默认隐藏 `user_id`，接收接口未显式补出该字段，导致浏览器消费者的归属校验跳过所有待发记录。此前 Chromium 测试 Fixture 带有该字段，未覆盖真实序列化差异。

- 在注册的真实 feed 路由测试中增加归属字段断言，修复前 Local/Desktop/Cloud/默认模式均稳定失败。
- 接收响应显式返回已认证用户 ID，保留 ORM 默认隐藏和浏览器归属校验；同步 API 文档、OpenAPI 与生成客户端，无数据库迁移。
- 修复后文件 SQLite 和 PostgreSQL 均通过 `go test -race . -run '^TestNotification(Browser|Desktop|GlobalClose)' -count=1`。日志：`/tmp/lazymind-browser-identity-{before,sqlite,postgres}.log`。
- Safari 实际点击“允许系统通知”后出现网站授权弹窗，允许后页面显示“已获得浏览器通知权限”。授权按钮工作正常，任务渠道开关不能代替浏览器网站授权。
- 确认无运行中任务后仅重启 Local Core。两次既有验收运行各自生成的通知均从 pending 变为 sent，并各有一条真实 `browser / delivered` 回执；未手动修改数据库状态或伪造回执。Local 总体状态为 ready。
- OpenAPI 四组生成检查全部 fresh，`git diff --check` 通过。Safari 浏览器 show 回执已验证，但不据此宣称实际看到了 macOS 横幅、验证了最小化时延或系统通知点击。

## 复现 Chromium 验证

已有 Playwright 可直接使用；本机命令（不安装生产依赖）：

```sh
cd /Users/zouyu/Downloads/LazyMind-notifications-official/frontend
PLAYWRIGHT_MODULE=/Users/zouyu/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright/index.mjs \
/Users/zouyu/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node scripts/browser-notifications.e2e.mjs
```

脚本默认使用 `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`，其他环境可通过 `CHROME_PATH` 指定。测试结束关闭临时浏览器和 HTTP 服务。

## 尚需真机验收与交付边界

- Chromium 使用无头模式；浏览器 show 回执不证明 macOS 实际显示了横幅。系统通知中心、最小化窗口时的实际时延、真实系统通知点击，以及 Firefox/Windows 尚需人工验收；Safari 已完成真实授权和 show 回执联调，系统界面展示仍需人工确认，步骤见 checklist.md。
- HTTPS/本机安全上下文、浏览器和系统通知权限是前提，系统勿扰/浏览器休眠仍可能阻止或延迟展示。
- 多标签页去重限同一浏览器配置、同站点和同一用户/租户。未实现多设备广播或浏览器与 Electron 跨进程统一抢占。
- 清除站点 localStorage（包括原有退出登录逻辑）会移除未确认的客户端记录；服务端已确认记录仍不返回。系统提交与回执不能构成原子事务，不确定结果不自动重弹。
- 本轮未提交或推送 Git，未重建安装包。Local 已运行当前前端并重启更新 Core，使用既有通知验收数据目录；原有默认数据目录未修改。其他 Compose 部署和 Desktop 安装包不会自动更新。
