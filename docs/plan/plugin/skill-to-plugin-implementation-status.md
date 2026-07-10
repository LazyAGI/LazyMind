# Skill → Plugin / Repair 实施状态

更新日期：2026-07-10

## 已实现

- 固定并记录 Skill head revision、revision number 和 tree hash。
- 从固定 revision 读取完整文件 manifest、文本文件和脚本，而非只读 `SKILL.md`。
- 普通 Skill 的候选确认会按固定 revision 重新读取，不在分析表长期复制完整大包。
- 生成前 suitability 分析，支持 `generatable`、`needs_confirmation`、`rejected`。
- 候选流程查询、用户确认、过期 version/revision 防护和前端确认入口。
- 大 Skill 有界分批 evidence 提取；超过处理预算的文件明确标记 `unresolved`。
- 全文件 coverage ledger；存在 unresolved 时不能宣称完整生成。
- Python AST 脚本分类：`importable_tool`、`wrappable_command`、`supporting_script`、`unsupported`。
- 安全 Skill 脚本在 Phase 3 原样复用，并再次经过现有安全扫描和 import dry-run。
- 不安全脚本隔离：跳过脚本、移除声明/step tool 引用、继续其余生成并汇总 warning。
- 工具 capability 元数据，以及基础设施/供应商绑定的分析规则。
- 框架工具 replacement 会移除被替代脚本，并确定性替换 state tool 引用。
- 只有 hash 与分析期安全报告一致的 Skill 脚本才允许普通用户发布。
- Repair 支持 `plugin_local`、`source_aware`、固定来源 analysis/revision。
- Repair draft version 乐观锁和 stale 防覆盖。
- Repair 前后跨文件诊断、Repair run 记录和查询接口。
- 前端自动携带 Repair draft version/source analysis。

## 尚未完成

### P0：上线前必须完成

- 将分块/evidence 缓存持久化。目前一次分析内会分批处理，但任务重试仍会重新调用模型。
- 为 suitability、coverage、供应商工具判定增加固定样例和算法单元测试，降低 LLM 输出漂移。
- 给脚本“命令式 main → 薄函数包装”实现真正的转换器；当前只分类并复用，未自动重构 main。
- 对 skipped unsafe script 的“是否为候选流程核心能力”增加确定性依赖检查；当前主要由 analysis verdict 判断。
- 补数据库迁移的 PostgreSQL smoke test 和 rollback test。

### P1：功能完整性

- Repair `scripts` 和 `full` target；当前主要覆盖 state/UI/scenario，脚本只参与诊断。
- Repair preview API 和文件级 patch/diff；当前记录修改文件，不保存逐行 patch。
- 工具 capability catalog 为全部工具补齐输入输出 schema、provider/product 身份和用户可用状态。
- 对 framework tool 不可用的情况增加发布/运行前阻塞，而不只是分析提示。
- 二进制文档的可插拔文本提取器；当前只记录 binary metadata。
- 内置 Skill snapshot 也改为不可变 revision/blob 来源；当前未安装模板仍从本地目录临时构建。

### P2：工程收尾

- OpenAPI registry 和 generated frontend client 更新。
- 新状态、候选确认、脚本忽略和 Repair 诊断的完整 i18n。
- 前端展示 coverage、tool mapping、script report 和 Repair diff，而不仅是候选按钮/错误提示。
- 生成/拒绝/工具替换/脚本忽略/Repair stale 的指标与灰度开关。
- 清理旧 `skill_content` 兼容字段；description 生成仍需保留。

## 粗略完成度

- 来源版本与完整包：90%
- Suitability/候选确认：80%
- 超上下文：75%
- 工具映射：70%
- 脚本处理：75%
- Repair：75%
- 前端与工程收尾：45%

整体约 75%。剩余工作主要是持久化 evidence 缓存、命令式脚本包装器、Repair scripts/full、完整 capability schema、前端报告可视化和 OpenAPI/i18n 收尾。
