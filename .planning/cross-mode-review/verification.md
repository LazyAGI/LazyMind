# Cross-mode review 修复与验证（2026-09-21）

## 获取版本
- 仓库：YuZou-coding/LazyMind
- 分支：codex/notifications-official-main
- 新克隆及结束前 `git ls-remote` 均确认远端 tip 为 `0721097199e93ad511c8e2481db5c90a2704521e`。
- 本地目录：`/Users/theone/Downloads/lazymind`。修改未提交、未推送。

## 修复
- **F1**：登录分配 session ID；刷新结果写入会话专属 overlay，不再读当前 B 资料并把 A token 合并到活动指针。检查后发生另一标签页写入也不会污染新会话。刷新请求按会话去重；可用时使用 Web Locks 串行化同会话轮换。请求/响应/重试标记会话；旧成功/失败不得触发新账号消费者或登出。logout 在网络等待前清空旧状态；local-admin 恢复结果检查发起代际。preload 同步及恢复使用同一 overlay 格式。
- **F2**：网页和 native 保留跨轮有界扫描 cursor；到尾部/重复 cursor 重置，作用域变化隔离，422 失效 cursor 重新扫描；暂时网络失败保留进度。不清 journal，不重发 unknown，不伪造 receipt。
- **F3**：desktop ack 最终 UPDATE 原子加入 `status <> 'skipped'` 条件，保留并发关闭的状态/原因；真实 receipt 可保留审计。
- **F4**：start/stop/recover 使用同一个原子 mkdir 互斥，覆盖依赖安装及 ready 等待；Vite+watcher 活着但 Electron 暂缺时拒绝重复启动；PID 记录启动时间，停止及强杀前校验身份。不自动回收遗留锁、不停止未经身份记录验证的旧 PID。
- **F5**：exit callback 比较 spawned child 对象身份；SIGKILL 后等待该 child 的 exit，不能仅发送信号就开始下一次热重启。

## 验证证据
先运行新增回归验证失败，再修复：旧刷新、提交边界账号替换、网页/native 2000/2001 饥饿、SQLite 注入 ack 最终写入前关闭、重叠生命周期操作、旧 child 迟到退出、过期 cursor、local-admin 迟到恢复。

通过：
- `pnpm --dir frontend test src/components/auth.test.ts src/components/request.session.test.ts src/modules/notifications src/runtime --exclude '**/AccountManagement.test.tsx'`：16 文件 / **113 tests passed**。存在既有 React/jsdom warning。
- `node --test desktop/scripts/native-notifications*.test.mjs desktop/scripts/dev-runner.test.mjs desktop/scripts/desktop-dev.test.mjs desktop/electron/src/*.test.js`：**110 tests passed**。
- 最后一次 PID 身份记录调整后重跑 lifecycle + watcher：**8 tests passed**。
- `cd backend/core && go test . ./taskcenter ./scheduler -run Notification -count=1`：三个包通过。
- `pnpm --dir frontend build`：通过，存在体积/依赖外置 warning。
- `pnpm --dir frontend typecheck`：通过（项目此脚本只检查指定 MCP 范围）。
- 使用 `/tmp/lazymind-review-tsconfig.json` 对本次 auth/request/browser/localSession 及新增认证测试的依赖图单独 tsc：通过。
- `bash -n desktop/scripts/desktop-dev.sh`、`git diff --check`：通过。

不能称全量全绿：
- `pnpm --dir frontend typecheck:all` 被未改动的 `src/modules/chat/utils/message.test.ts:49` TS1128 阻断。
- 扩大通知测试发现 `AccountManagement.test.tsx` **7 个失败**；用 `git archive HEAD frontend` 得到的原始源码副本和同一依赖树复现同样 7 个失败。
- 桌面全量 217 tests：214 通过，3 个 `desktop-build.test.mjs` 源码形状断言失败。针对这三个用原始源码副本复现失败（hidden renderer recovery、background/quit、home readiness）；不是本次修改引起。

## 验收边界与运维说明
- 未运行真实 Electron/Vite/Runtime，没有向真实用户发通知或结束用户服务。
- PostgreSQL READ COMMITTED 双事务并发未实测。F3 使用真实 SQLite handler + 最终 UPDATE 前的确定性状态注入，验证 WHERE 条件，而非声称 PostgreSQL E2E。
- 未做 macOS 通知中心、Windows 或真实账号/渠道 E2E。
- 进程身份校验减少 PID 复用风险，但 shell 的 ps/kill 不是 OS 原子 handle，不声称消除了所有极端 PID 重用 TOCTOU。
- SIGKILL/断电可能遗留 `local/build/desktop-dev/transition.lock`；必须确认没有 start/stop 操作后再由使用者清理，脚本不会自动破锁。旧版只有 PID 无 `.identity` 的会话需人工确认后处理。
- 正常 hot restart 空窗不再被回收；watcher 存活但 Electron 已故障时，应显式 `make desktop-dev-down` 再启动。


## Follow-up — existing failures, UI unchanged (2026-09-21)
- Reproduced 7/7 AccountManagement failures and 3/39 desktop-build failures before edits.
- History and implementation confirm removed remark/authorization-details UI and simplified account labels; old tests described obsolete UI. Desktop now deliberately waits for parser readiness rather than the home-only readiness race.
- Changed only AccountManagement.test.tsx and desktop-build.test.mjs (plus planning notes) in this follow-up. No production code, UI, styles, translations or desktop startup behavior changed.
- Replaced obsolete UI expectations with current name/unbound state, absence of removed editor, and actual same-name selection by account ID. Preserved confirmation-before-delete, target-only deletion, connected/paused deletion prohibition and dependency-failure blocking assertions. This does NOT restore a remark editor or visually distinguish identical names with extra identity text.
- Desktop checks retain one-time renderer recovery and quit/background guards, and assert parser readiness before renderer creation. Existing renderer-recovery behavior tests also pass.
- Validation: `pnpm --dir frontend test src/components/auth.test.ts src/components/request.session.test.ts src/modules/notifications src/modules/channelGateway src/runtime` — 19 files, 124 passed.
- Validation: `node --test desktop/scripts/*.test.mjs desktop/electron/src/*.test.js` — 220 passed, 0 failed, 0 skipped.
- `git diff --check` passed. No live Electron/native E2E performed. The separate previously reported full typecheck syntax error is outside this follow-up; no claim of whole-repository green.
