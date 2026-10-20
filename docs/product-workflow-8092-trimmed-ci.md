# 8092 精简版产品 Workflow：来源与本地 CI

## 最终基线及范围

- 日期：2026-09-29，Asia/Singapore；macOS ARM64。
- 目标：CarlosShaoting/LazyRAG `workflow_dev`，基线 `cebaa36acd3bf130da2ba7a509caeb6e5ccaedf4`。
- 代码来源：本地 `work/product-aligned-8092` 的 41 文件精简候选，原审计为新增 8,278、删除 757，共 9,035 行。
- 对齐目标后：41 个代码文件，新增 8,285、删除 764，共 **9,049 行**；本记录单独计入文档增量。
- 41 个代码文件中，39 个与本地候选逐字节一致。`workflow_manager.py` 保留上游已有差异；`WorkflowPanel/index.tsx` 保留上游自动跟随、当前步骤标记，并接入产品成果组件。
- 旧 PR #6 曾合并到 `c186ca8d`，之后上游强制更新到上述基线，已不包含旧产品实现。因此本 PR 直接添加精简版，不再携带旧实现或撤回提交。
- 不修改 PPT/图片 Workflow 包、数据库迁移、依赖锁文件或子模块版本；不提交本地 Python 回归测试文件。

## 功能边界

保留产品两层路由、同一项目跨阶段复用、对话共享工作区、关键决定确认、阶段成果展示与发布校验。
产品 UI 通过 `ProductProject`、`productPresentation` 和 `ProductTaskProgress` 接入原有工作流面板/任务中心。
隐藏产品内部开始、方案大纲、交付总结及检查产物，沿用本地精简版展示规则。

B03、B04、B05 经过共享执行器承载点，但产品策略仅在 `product_solution_delivery` 且满足 publisher-owned 契约时启用。
其他 Workflow 保留上游默认轮数、工具配额和执行路径；产品策略模块及调用处有对应注释。

## 本地 CI 通过记录

工具版本：Node v26.7.0、pnpm 11.19.0、Go 1.25.14 darwin/arm64。
以下检查针对本次最终代码树执行，均在更新 PR 分支前完成。

| 工作目录 | 命令 | 实际结果 |
| --- | --- | --- |
| 仓库根目录 | `GITHUB_BASE_REF=upstream/workflow_dev make lint` | PASS，Python/Go lint、状态后端边界、命名、迁移不可变等检查通过 |
| 仓库根目录 | `python3 -m pytest tests/doc_check -q` | PASS，2 passed |
| frontend | `pnpm typecheck` | PASS；这是仓库的 tsconfig.mcp.json 检查，不等同于 typecheck:all |
| frontend | `pnpm lint` | PASS |
| frontend | `pnpm test:workflow-panel` | PASS，18 文件、156 tests |
| frontend | `pnpm build` | PASS，OpenAPI freshness 通过，Vite 生产构建完成 |
| backend/core | `go test ./workflow/... ./doc ./skillv2/...` | PASS |
| backend/core | `go test ./... -run '^$'` | PASS，Core 全包编译；此命令不执行全部测试 |
| backend/core | `go run ../../../router-fix/compile.go ../../workflows/product_solution_delivery` | 使用本地 graphengine 编译驱动执行 ProfilePublish：valid: true，工具不入 PR |
| 仓库根目录 | `../test-venv/bin/python -m pytest ../pr8-final-local-tests -q` | PASS，219 passed in 6.98s |
| 仓库根目录 | `git diff --cached --check` | PASS，无冲突标记或空白错误 |

本地 Python 回归来自 `work/verify-release-8092/python/test_product_*.py` 的独立副本，
只将其候选源码路径改为本次 `work/ci-lazyrag-8092`，断言保持不变，副本存放在仓库外的 `work/pr8-final-local-tests`。
覆盖产品契约、路由、共享工作区及写作产物行为；这 219 项不是远程模型端到端验收。
图编译工具实际路径为 `work/router-fix/compile.go`，通过仓库现有 graphengine API 验证 YAML 发布图。

构建输出包含 Sass 弃用、浏览器 externalized 模块、资源路径/包体大小和 macOS SDK 弃用警告；进程退出码均为 0。
本记录不将这些本地检查表述为 GitHub Actions 全矩阵通过，也不代表重新部署 8092 后的真实模型端到端验收。
