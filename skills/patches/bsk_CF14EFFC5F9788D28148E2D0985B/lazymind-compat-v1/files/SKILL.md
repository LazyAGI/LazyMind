---
name: tender-document-smart-parser
description: 检索招标项目并解析公告正文，提取预算、技术要求、交付付款条件与投标风险，核对附件及原文链接。
version: 1.0.6
---

# 招标文件智能解析

## 使用边界

需要用户提供的 BBIAO_API_KEY。优先使用运行环境变量，或用户指定的本地 JSON 凭证文件（字段 BBIAO_API_KEY）。不要显示密钥，不要把密钥写入报告、示例或提交文件。未配置时说明所需凭证，不伪造检索结果。

## 工作流程

1. 按用户关键词与日期检索；未给日期时使用最近30天。通过 run_script 执行 scripts/tender_client.sh（调用系统 python3），不另建 API 技能、不把 POST 接口交给网页 GET 工具。此入口避免宿主打包程序被误用作 Python 解释器，需要系统已安装 Python 3。
2. 从检索结果选择相关项目，沿用返回的 id 与 publishTime；分别查询详情、附件、原始公告地址。
3. 解析正文中明确出现的预算、采购方式、技术门槛、时间节点、交货、质保与付款条件，形成应标核对表。
4. 区分原文事实与分析建议。搜索摘要与正文不一致时以正文为准并注明；公告结束时间不自动等同于投标截止时间；未披露的资格、评分条款应写“需原文或附件补充”。
5. 附件列表为空不代表调用失败：交付公告正文解析，说明未取得附件，不声称完成完整招标文件审查。有附件时再用现有文件读取工具读取；不能读取则清楚标出范围。
6. 保存 Markdown 报告到会话工作区，给出可打开的文件链接。

## 调用

run_script 的 name 使用 list_skills 返回的准确名称，rel_path 为 scripts/tender_client.sh，args 必须为字符串数组。不要设置 cwd 到 Skill 外部。凭证文件由脚本直接读取，无需先 list_dir 或 read_file 检查。

- 检索参数：`["search", "--keyword", "人工智能服务器", "--start", "2026-09-01", "--end", "2026-09-15"]`（日期随任务调整）
- 项目详情：`["detail", "--id", "检索返回的ID", "--published", "检索返回的发布时间"]`
- 附件：将 detail 替换为 files；原文地址：替换为 source。
- 本地凭证文件：在以上 args 开头加 `["--credential-file", "用户指定的绝对路径"]`，不要读取并回显文件内容。

脚本用 Python 标准库，固定向官方 HTTPS 网关发送 POST JSON，自动处理 detail/source 的 id、files 的 projectId 和搜索 pageId/pageNumber。HTTP成功不等于业务成功，必须检查输出 ok。

## 失败处理

- missing_api_key / invalid_credential_file：请用户配置凭证，不进入循环重试。
- 业务403或0100590001：认证或权限问题，停止该接口调用，记录明确返回信息；不要无证据归因于平台脱敏。
- 0100590006：额度不足，停止调用。
- network_or_invalid_response：本次请求失败，可重试一次，仍失败则交付已获取部分并说明缺口。
- 绝不提交投标、支付、签署或自动联系采购方。本技能仅做信息检索与辅助分析。
