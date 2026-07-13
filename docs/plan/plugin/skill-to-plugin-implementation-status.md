# Skill → Plugin / Plugin Repair 实施状态

更新日期：2026-07-10

## 已完成

### 来源版本与完整包

- 生成始终固定当前生效的 Skill head revision，并记录 revision id、revision number、tree hash。
- 从固定 revision 加载完整目录 manifest、文本、脚本及文件元数据，不再只读 `SKILL.md`。
- 普通 Skill 在候选确认时重新读取同一不可变 revision，并校验 tree hash；内置 Skill 也生成并校验目录 snapshot tree hash。
- Plugin 版本管理、来源分析和 Repair 均保留 Skill/Plugin revision 关联，避免 head 漂移。

### 可生成性判断与拒绝

- 独立 suitability 阶段返回 `generatable`、`needs_confirmation`、`rejected`，并持久化 verdict、候选流程和证据。
- 没有可执行步骤、只有规范/约束/偏好、或只是无流程关系的并列工具合集时，可明确拒绝，不进入状态机生成。
- 多个合理流程必须由用户确认；确认接口会校验用户、analysis、固定 revision 和 tree hash。
- 同一 Skill revision/tree 的最终分析结果可复用；`reanalyze=true` 可显式强制重分析。

### 大 Skill 与信息完整性

- 对完整 manifest 做有界分批分析，建立逐文件 coverage ledger。
- 文件状态区分 reviewed、supporting、binary、unresolved；超预算内容不会被静默遗漏。
- 存在影响流程的 unresolved 内容时不能宣称完整生成，分析会要求确认或拒绝。
- source package 只持久化 manifest/摘要，避免把超大包重复写入分析记录。

### 工具映射

- 工具目录包含 capability、scope、provider/product、可用状态、输入/输出 schema 和 required config。
- 基础设施类工具按同定位能力替换，即使底层选型不同；云服务/供应商绑定工具只有同一产品才视为等价。
- 已有同定位框架工具会向用户报告“跳过原工具，使用框架工具”，并确定性替换状态机引用。
- 分析期记录 replacement availability；发布时缺失/不可用会 fail closed，运行加载时也校验 `required_framework_tools`。

### 脚本处理与安全

- Python AST 分类为 `importable_tool`、`wrappable_command`、`supporting_script`、`unsupported`。
- 工具函数保留显式函数签名并作为插件脚本复用；命令式 `main` 自动生成显式参数的薄包装函数。
- `python scripts/xxx.py` 类型调用转换为插件可调用函数，状态机引用同步更新。
- 安全脚本经过静态安全扫描、import dry-run 和内容 hash 审计；发布只接受与分析报告一致的脚本。
- 不安全/不支持脚本被隔离并忽略，相关声明和 step tool 引用被移除；其余生成继续进行，最终统一返回 warning，而不是让整项任务失败。
- Repair 的 `scripts` 与 `full` target 采用同样的隔离策略。

### Plugin Repair

- 支持 `plugin_local`、`source_aware`，并固定来源 analysis/Skill revision。
- 支持 state machine、scenario、UI、scripts 和 full target；full 按跨文件顺序修复并统一诊断。
- Repair 使用 draft version 乐观锁，防止过期结果覆盖用户新编辑。
- 提供 repair preview、修改文件清单、前后诊断、warning，以及 repair run 查询记录。

### API、前端与工程保障

- OpenAPI registry 已登记 analysis 查询、候选确认、repair preview、repair run。
- 前端展示候选确认、coverage、tool mapping、script report、脚本忽略提示和 repair preview。
- 中英文 i18n 已覆盖新增状态和提示。
- 数据库 migration 同时提供 up/down，并有 schema contract/rollback contract 测试。
- 已有回归测试覆盖固定 revision 包读取、诊断、脚本 hash 发布门禁、框架工具可用性门禁和 OpenAPI 注册。

## 尚需部署环境验收（不属于代码功能缺口）

- 在实际 PostgreSQL 实例执行 migration up/down smoke test；仓库测试环境目前只做 SQL contract 校验。
- 在完整 Python 运行环境执行端到端生成/Repair 样例。当前工作区的 `algorithm/lazyllm` 子模块存在用户已有的不兼容改动，直接 import 会在 `Config.add(alias=...)` 处失败；本次修改文件均已通过 `py_compile`。
- 按上线平台接入业务指标面板和灰度配置中心；代码路径已有结构化状态、verdict、warning、repair run，可直接作为采集源。

## 完成度

计划内核心代码与产品流程已完成。剩余事项均为真实部署依赖的 smoke/灰度运营验收，不影响本次代码合入。
