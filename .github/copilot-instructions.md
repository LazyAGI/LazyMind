# DSH workflow 修复执行说明

本文件由 doc-and-modernize skill 生成，适用于 `docs/plan/dsh-fix/MODERNIZATION_PLAN.md` 的实施；其他任务继续遵循其实际范围和现有仓库规则。后续维护优先阅读 `docs/plan/dsh-fix/IMPLEMENTATION.md` 与验证记录；原计划和 ARCHITECTURE.md 保留为设计历史。

## 任务与分支

- 用户指定 `ch/dsh_fix`，从最新上游 main 创建，已快进引入 PR #698。按该计划的一条修复 PR/顺序提交执行，不自动改成多分支堆叠。
- 预定修复 PR base 为作者 `workflow/DeepSeekHarness_integration`；发布前再次核对作者分支与 main。不要默认推到 upstream/main。
- 实现和实测状态以 VALIDATION.md 为准；不能把设计或 mock 通过当成生产验证。

## 已有命令

| 目录 | 命令 |
| --- | --- |
| frontend | `pnpm install --frozen-lockfile`、`pnpm test`、`pnpm run typecheck:all`、`pnpm run build`、`pnpm run lint`、`pnpm run gen:openapi:check` |
| local/lazymind-cli | `go test ./... -count=1` |
| backend/core | `go test ./workflow/... -count=1` |
| backend/core | `go test ./migrate -run '^TestRepositorySQLiteReleaseAndDevPathsMatch$' -count=1` |
| repo root | `make lint`、`bash scripts/test_migration_upgrade.sh upstream/main`、`git diff --check` |
| integrations/dsh-workflow | `pnpm typecheck`、`pnpm test`、`pnpm bundle`，同步更新 CLI 内嵌归档 |

默认 frontend `typecheck` 仅覆盖 scoped tsconfig，不能用于证明本次全部修改类型正确。既有基线失败要记录，与本次新增错误区分；不关闭相关检查来获得绿灯。

## 阶段门槛

- P1 建立精确依赖、DSH SDK 契约与实际目标分支的 CI。正式可测试前只使用已经取得的源码/行为/回放证据，不能要求不存在的 CI 脚本通过。
- 正式可测试后，每个工作包都以计划对应的 unit/contract/数据库/真实宿主门槛退出。最高目标 L4；低于它要记录残余风险及关闭阶段。
- CI 文件可在实现中编写；作者 fork Actions 授权、required checks/branch protection 由维护者配置，不能写成已完成。
- 业务权威在 Core；连接器不保存审批布尔值，DSH 私有密钥不读入，未知投递不盲重发。纯规则模块不得导入宿主/HTTP/ORM 类型；事务与框架代码保留在对应实现层。
- PG/SQLite migration 必须遵循 `backend/core/migrations/AGENTS.md`；不改已经合入的 dev migration，不凭空新增 aggregate。
- 拓扑、命令、接口或支持能力改变时，同 PR 更新本文、计划、README 和架构。修改范围以需求为准，不顺带做全仓库重写。
