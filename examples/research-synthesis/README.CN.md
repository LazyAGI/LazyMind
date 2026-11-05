# 从访谈记录到可审阅的研究报告

[English](README.md)

把六份合成访谈整理成带来源的发现与决策建议。LazyMind 导入真实 `synthesize-research` Skill，由模型自动生成并发布 Workflow，再执行显式绑定的研究材料。没有手工编写 Workflow YAML 冒充自动转换。

## 实际结果与版本

查看 [最终报告](result.md) 和 [精简运行记录](run-summary.json)。2026-11-05 使用 `deepseek-flash` 验证：Core 为 [PR #921](https://github.com/LazyAGI/LazyMind/pull/921) 的 `405f31ae`，Algorithm 为 `59d2a836`，LazyLLM 为 `89f4e031`。额外两项 Algorithm 修复位于[独立审查分支](https://github.com/NicoYang1984/LazyMind/tree/codex/skill2workflow-execution-contracts)，不混入本 Example PR；未验证未修改 main 能否通过。构建测试版本时遵循项目 Quick Start，并隔离数据和端口。

自动生成五步：证据整理 → 主题归纳 → 发现排序 → 机会建议 → 报告。末步 Attempt 为成功/失败/失败/成功；明确授权的用户重试后，又通过公开 Artifact 编辑接口压缩并纠正交付物。最终 Session 完成，两个选中 Artifact 可读。可展示带审阅的流程，不宣称一次自主成功。

## 来源与复现

来源为 Anthropic [knowledge-work-plugins](https://github.com/anthropics/knowledge-work-plugins/tree/2574e29377c7173d0cecffef29a6ecbd7f8ccb5d/product-management/skills/synthesize-research)，固定 Commit `2574e29377c7173d0cecffef29a6ecbd7f8ccb5d`，Apache-2.0 许可。打包时仅将父目录引用改成 `references/CONNECTORS.md`，同时包含依赖、LICENSE 和 [来源及 Hash 清单](ORIGIN.json)。不暗示上游官方认可或合作。

在启用 Hosted Conversion 的隔离实例中配置支持工具调用的对话模型，通过正常登录取得测试用户 Token，然后按 [英文说明](README.md#reproduce) 执行。模型调用消耗供应商额度。

`prepare_request.py` 下载固定版本、校验 Hash、构建确定性 ZIP 并生成新请求。`research_data` 和 `research_scope` 显式绑定到输入文件；`reuse_workflow=false` 要求重新转换。提交遇到网络中断时重用同一请求文件；独立实验使用新目录。

记录实际 Revision、Session、每步 Attempt，并读取最终 Artifact。遇到输入绑定、审批或人工审阅状态时按产品正常流程处理，不绕过门禁。生成文件原样保留，不以手改 YAML 作为转换成功证据。

## 业务验收

以 [研究输入](research-data.md) 为准，检查六人的原话、首次使用4人/熟练用户2人的分类，以及主题频次：入口发现3/6、上传进度2/6、可访问性1/6。P06 已用键盘找到入口，不能写成所有首次使用者都找不到入口。报告应提出两周验证计划，而不是声称改进已经实施。小样本不能外推总体比例。

流水线完成不等于事实全部正确；模型生成的步骤和文案可能变化。本例不证明命令执行、外部连接器、审批或 Retry 的全面可靠性，也不宣称比原生 Skill 更快。纠错时保留旧 Attempt 和 Artifact。

## 本例包含真实审阅纠错

首次五步运行完成后，独立来源审阅发现模型补出了未记录的完成结果和访谈条件。通过公开 Workflow 回退操作重跑末步，明确传入事实纠正，保留旧 Attempt 和 Artifact。本例展示带审阅的交付，不宣称研究全程无人干预且事实零错误。纠错后读取 Session 当前选中的 Artifact，同时检查 Session 状态，不仅看外层 Task。

## 75 秒演示脚本

- 0–15 秒：展示六份合成访谈和业务问题——团队下一轮应先改什么？
- 15–30 秒：展示固定来源、依赖打包和真实自动转换记录。
- 30–45 秒：展示生成的步骤与中间证据，指出输入与结果如何关联。
- 45–65 秒：打开真实最终报告，核对原话、人数与 P06 反例。
- 65–75 秒：展示本次实际验收结果；如有重试或人工纠错，明确说明，不剪辑成一次自主通过。

执行 `python3 check_report.py result.md` 可检查来源 ID 和原话；该脚本不替代上述事实审阅。
