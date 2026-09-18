---
name: feishu
description: 在 LazyMind 中读取、总结、创建、追加或修改飞书/Lark 文档与 Wiki 页面。用户提供 feishu.cn 或 larksuite.com 的文档链接，要求读取正文、写入内容、更新指定段落、创建文档，或同时要求用浏览器打开/截图时使用；普通公开网页不要使用。
version: 1.2.1
category: productivity
tags:
  - feishu
  - lark
  - cloud-document
license: MIT
metadata:
  source: https://github.com/leemysw/feishu-docx/tree/b12a543399d165929c7f1df2a00d826356d53f39/.skills/feishu-docx
---

# 飞书文档

使用 LazyMind 已连接的飞书 OAuth、`CloudFileToolkit`、AI Writer 或 Browser Tool 完成文档操作。不要安装或调用上游 `feishu-docx` CLI，也不要把飞书私有链接交给通用 `url_fetch`。

## 路由

- 读取、总结或分析：展开 `CloudFileToolkit` 和飞书供应方，保留完整 URL，先调用 `FeishuWikiFS_resolve_link`，再调用 `FeishuWikiFS_read_with_references`；只需要正文时可用 `FeishuWikiFS_read`。
- 替换一个已存在的文本块：先用 `FeishuWikiFS_get_doc_blocks` 取得 `block_id` 和 `plain_text`，唯一定位目标块后调用 `FeishuWikiFS_update_doc_block_text`，再读取该文档验证结果。目标不唯一时先询问用户，不能猜 block id。
- 创建文档、追加正文、替换全文或执行多块结构化修改：默认调用 `trigger_writer_workflow`，将用户原始请求中的完整飞书 URL、目标内容和“追加/替换/修改”语义原样保留。Workflow 启动后按其 Ready steps 继续。
- 用户明确要求“用浏览器插件”“通过页面操作”，或要求复用浏览器中的飞书登录态时，整个读取/编辑/校验流程都走 Browser Tool，不要在中途改走飞书 API 或 Writer。执行前阅读并遵循 [浏览器 UI 编辑流程](references/browser-ui-editing.md)。
- 用户只要求用浏览器打开或截图、没有要求修改正文时，调用 `browser_open`；需要截图时继续调用 `browser_screenshot`。打开或截图成功不代表正文已经修改。

## 完成条件

API/Writer 路径依赖已连接且授权的飞书账户；浏览器路径依赖 Browser 扩展在线且受控页面中已有可用登录态。未登录、未连接、授权过期或权限不足时，停止写入并给出具体提示，不降级为匿名网页抓取。

用户已经明确要求写入、创建或修改时，直接执行目标操作，无需重复确认。API/Writer 路径在工具返回后重新读取目标文档，只有回读内容与请求一致才能报告写入成功。浏览器 UI 路径以带 `verify_text` 的输入工具成功返回作为页面写入完成条件，按浏览器参考流程立即停止，避免重复截图、快照或检索。保留工具返回的文档链接，最终说明使用了 API/Writer 还是浏览器 UI，以及完成了哪种校验。

## 来源

本 Skill 参考并适配 MIT 许可的 `leemysw/feishu-docx` Agent Skill；来源版本、改动边界和许可证见 [SOURCE.md](SOURCE.md) 与 [LICENSE](LICENSE)。
