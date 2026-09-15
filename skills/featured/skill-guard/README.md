# Skill 安装前审查 — 接入记录

来源：https://skillhub.cn/skills/clawhub_jamesouttake/skill-guard

本地适配使用标准库脚本锁定发布者、隔离下载并登记哈希，替换原流程对 ClawHub CLI 裸 slug 和自动安装的依赖。保留“安装前审查”的任务目标，但不宣称本地启发式规则等价于远程语义扫描。来源在详情说明展示，不额外注入模型正文。

## 实测与展示范围

2026-09-14 至 15 日，LazyMind 对 `https://clawhub.ai/paudyyin/skills/summarize` 完成实际下载，产生 5 个文件的清单与 JSON 检查报告；模型根据源码给出接入评估并保存 Markdown 报告。首次运行发生工具回执超时及模型 transport_error，沿用已生成证据续接后完成报告。

`assets/demo.html` 根据真实 JSON 与模型报告整理展示。记录是 AI 审阅意见，不是人工安全认证。远程 Snyk 扫描、下载代码动态执行均未测试，目标 Skill 未安装。

`tests/test_assess.py` 覆盖发布者保留、非法来源、ZIP 路径穿越、二进制标识、发布者不匹配与不执行/不安装约束。

封面为适配时生成的示意图，不是扫描证明。仅本地预览；公开分发前需确认上游的授权/许可证条件。
