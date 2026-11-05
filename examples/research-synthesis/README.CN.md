# 研究综合：Skill → Workflow → 审阅后的研究报告

[English](README.md) · [最终报告](result.md)

本例将 Anthropic 的 `synthesize-research` Skill 导入 LazyMind，由模型自动转换并发布五步 Workflow：证据整理 → 主题归纳 → 发现排序 → 机会建议 → 报告。输入是六份合成访谈，交付物是带来源的研究决策备忘录。没有手写 Workflow YAML。

**本例已完成带用户参与的端到端验收。** 审阅、授权重试和 Artifact 编辑都是流程的一部分；不要求模型一次零错误。自动执行的首稿有事实扩写，末步两次失败后经用户授权重试成功，最后通过公开 Artifact 接口压缩和纠正报告。原始日志和模型草稿不纳入仓库。

## 复现

使用包含本 PR 修复的隔离实例，启用 Hosted Conversion，配置支持工具调用的对话模型，并通过正常登录取得测试用户 Token。按 [英文说明](README.md#reproduce) 执行；调用消耗供应商额度。

`prepare_request.py` 校验固定来源 Hash、补齐 CONNECTORS 父级依赖、打包并显式绑定 `research_data` / `research_scope`。`reuse_workflow=false` 要求真实新转换。提交遇到网络中断时重用原请求文件，新实验则使用新输出目录。

读取 Task 返回的 Session 和 Revision，检查每步 Attempt 及当前选中的 Artifact。回退期间外层 Task 可能仍显示此前成功，应以当前 Session 为准。审批或重试额度要求用户操作时，走正常产品流程。

## 验收

运行 `python3 check_report.py result.md` 检查来源 ID 和六条原话；再对照 [原始材料](research-data.md) 核验首次使用4人/熟练用户2人，入口发现3/6、上传进度2/6、可访问性1/6，以及 P06 用键盘找到入口的反例。P02、P06 的最终完成结果未知，不能补写为完成；建议仍是拟议行动。

2026-11-05 使用 `deepseek-flash` 验证，Core `405f31ae`、Algorithm `59d2a836`、LazyLLM `89f4e031`；额外两项 Algorithm 修复已包含在本 PR。Session `55ebc40e-393e-5070-931f-00ad4619829b` 最终 completed，公开 API 回读报告与仓库 [result.md](result.md) 一致。关键人数、频次与原话已核对。未验证 Approval，也不宣称所有模型、三个 Skill 或无人干预执行都可靠。

## 来源与演示

来源：[Anthropic knowledge-work-plugins](https://github.com/anthropics/knowledge-work-plugins/tree/2574e29377c7173d0cecffef29a6ecbd7f8ccb5d/product-management/skills/synthesize-research)，Apache-2.0。[ORIGIN.json](ORIGIN.json) 固定版本和原始/适配 Hash；打包只修改 CONNECTORS 相对路径，并包含许可文件。不暗示上游官方认可或合作。本例无需外部连接器。

75秒演示：15秒展示研究问题和输入；20秒展示来源及真实生成的五步；25秒打开报告核对原话、人数与反例；15秒展示用户审阅、重试和编辑。该例不用于宣称性能提升，也不展示命令执行能力。
