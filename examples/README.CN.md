# LazyMind 案例

[English](README.md)

继续使用熟悉的 Codex 或 DeepSeek Harness（DSH），通过 LazyMind 插件调用工作流和知识库；需要审核或编辑时，使用 LazyMind 提供的面板。

这里优先复用已有案例。宣传视频证明的是此前的演示，不能代替当前版本验收；每篇单独列出复现状态和缺失证据。

| 案例 | 要检查的收益 | 复现状态 |
| --- | --- | --- |
| [现有 Agent → 可编辑 PPT](agent-to-editable-ppt/README.CN.md) | 审阅中间材料、局部修改、交付可继续编辑的文件 | Codex 成片、局部修改及 Keynote 导出编辑通过部分验收；最终审核、DSH 实跑与独立复现待补 |

先阅读[连接现有 Agent](shared/agent-setup.CN.md)。用户仍需运行 LazyMind 并配置所需服务，插件不能脱离 LazyMind 独立执行。

案例目录只维护复现资料和记录，不复制工作流实现。生产实现保留在 `workflows/`，已有预览素材保留在 `skills/featured/`。
