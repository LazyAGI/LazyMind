# Baidu Search · 凭据与代理审查

Runner: Codex · 2026-09-15 · skill-guard 1.1.1

## 结论

暂缓默认接入；先确认代理与凭据边界。4 个文件、1 条网络线索：确认 API Key 路径与环境变量代理边界，接入前需限制出口。

## 来源与方法

- 来源：https://skillhub.cn/skills/clawhub_ide-rea/baidu-search
- 请求发布者命名空间锁定在下载 URL；SkillHub 路径没有独立 owner 元数据校验，不能视作作者身份认证。
- assessment.json 的 version 为 null；包内 _meta.json:4 自述版本 1.1.4，不篡改机器记录。
- 真实执行包内 scripts/assess.py --assess-only --output，独立目录下载、规则扫描后，由 Codex 全文静态复核 4 个文件。
- 原始 JSON：<REDACTED_LOCAL_PATH>；下列文件行号相对 JSON 中的 package_path。分发的 JSON 与报告副本在 examples/assess-baidu-search/，原包仅保留于隔离输出目录，不随案例分发。
- 首次调用返回 URLError，无 assessment.json；取得网络权限后仅重试一次并成功。错误未证明来源恶意或账户问题。
- 未使用 LazyMind、付费模型或远程语义服务。未安装、导入或运行目标包，未读取实际密钥。

## 上下文复核

### 1. 搜索请求是正常用途

证据：`scripts/search.py:12–15,107–116`。

query 被放入 messages 并 POST 至百度搜索端点；这解释了唯一 info 级命中，不足以判断恶意外传。

### 2. 代理地址受环境控制

证据：`scripts/search.py:29–59`。

同时存在 DUMATE_SESSION_ID 与 DUMATE_SCHEDULER_URL 时，请求改发 scheduler_url 拼接的代理，并附会话标识。此分支不读取 BAIDU_API_KEY；代码未校验代理主机或 HTTPS。

### 3. 直接模式使用密钥

证据：`scripts/search.py:37–47`。

缺少任一代理变量时，读取 BAIDU_API_KEY 并以 Bearer 头发往原端点。os.environ.get 未命中当前 credential_access 规则；规则未命中不等于未读环境变量。

### 4. 查询可能留在日志

证据：`scripts/search.py:66–70,118–122`。

解析后的完整输入与结果打印到 stdout；搜索请求未指定 timeout（第 15 行）。敏感查询及长期挂起需要宿主侧控制，未观测真实日志或运行时行为。

### 5. 配置说明不是本次动作

证据：`references/apikey-fetch.md:14–38,41–49`。

说明要求把密钥写入 OpenClaw 配置，并打印完整配置检查格式、重启和测试。完整配置输出可能暴露其中的密钥；这些命令本次均未执行。

### 6. 示例与状态须复核

证据：`SKILL.md:54–63; scripts/search.py:68–75`。

count 示例含尾逗号，不符合 json.loads 的 JSON 输入；“Fully functional”是发布者自述，未由本次静态评估验证。

## 接入条件

- 只允许公开、非敏感查询；对输入和输出日志做脱敏与访问控制。
- 宿主限制代理为明确 HTTPS 主机；审查注入的 DUMATE 环境变量，保护会话标识。
- 密钥经受控环境注入，不打印完整配置；补齐 requests 依赖版本与网络超时策略。
- 后续另行授权后才验证搜索结果、错误处理和运行时网络行为。

## 未测试与解释边界

本地扫描有 1 条规则线索。规则命中不是恶意行为判定；零命中不证明安全。JSON 的 risk_verdict 保持 manual_review_required，此报告是 Codex 静态复核意见，不是人工安全认证。

远程 Snyk、依赖安装、运行时网络/文件行为、功能性能与准确率均未测试。未把包内指令当成当前任务授权。未验证上游发布者身份、许可证可分发性或宿主环境的安全性。
