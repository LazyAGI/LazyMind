# 官方基线失败记录（本次不处理）

基线：LazyAGI/LazyMind main `35ea71810d17f622febc1c04caa8c1d9ec9cbb03`。

## 已复现

1. 官方原始 v0_3 aggregate 在文件 SQLite 上执行失败：`near "EXISTS": syntax error`。独立官方工作目录中的聚焦测试依次执行既有 aggregate，无通知改动；同样失败。原因是 SQLite 分支中的 vocabulary_review_sessions 使用两条 ADD COLUMN IF NOT EXISTS；SQLite 不支持该语法。通知迁移测试在 aggregate 路径也稳定复现，dev 和升级/回退路径通过。
2. 官方原始 SettingsPage.developer.test.tsx 失败：runtime/mode mock 未提供页面当前调用的 isVocabularyEnabled。隔离官方副本单独执行同样失败。

## 拟修改

- 仅调整已有 aggregate 的 SQLite 分支：将 status 和 expires_at 放入该分支已有 CREATE TABLE 的最终定义，删除同一分支紧随其后的两条不支持的 ALTER；默认值和最终字段保持一致。PostgreSQL 分支及所有已合并 dev migration 保持不变。
- 给官方设置页测试的 runtime/mode mock 补齐 isVocabularyEnabled，保持原有断言。
- 保留通知迁移与回归测试，重新验证文件 SQLite/PostgreSQL 的迁移、任务和通知；重新运行前端测试。修复单独归类，避免混入工作区功能。

## 复现命令及日志

- `/tmp/lazymind-official-baseline-20260917/backend/core`：`go test ./migrate -run TestOfficialSQLiteAggregateBaseline -count=1`。聚焦用例只调用现有 catalog、文件 SQLite 与迁移执行 helper。
- 同目录前端：Node 24 执行 `node node_modules/vitest/vitest.mjs run src/modules/settings/SettingsPage.developer.test.tsx`。
- 日志：`/tmp/notifications-official-baseline-migration.log`、`/tmp/notifications-official-baseline-settings.log`。

此文档记录提案，尚未实施以上两处修改。

## 最终范围决定

用户明确要求“不处理，只进行分支相关的改动”。上述两项修复提案不实施，不再作为分支隔离的收尾条件。保留官方原始 SQL 和测试，记录实际失败；不宣称全量测试通过。
