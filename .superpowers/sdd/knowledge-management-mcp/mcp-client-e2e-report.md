# 8091 真实 Chat / CLI 验收（进行中）

环境：仅 `http://127.0.0.1:8091`，主代理已备份、部署并验证 Core / Chat / frontend 来自本 worktree。2026-10-20 通过公开 `/_local/admin-session` 取得真实管理员会话；凭据、原始 SSE、脚本仅存在私有 `/tmp/lazymind-kb-e2e-20261020/`，不进入 Git。没有伪造身份头、直接创建数据库 run 或模拟模型。Kimi k3 使用每个新会话的固定模型选择，未更改全局默认配置。

## 已真实通过

| 场景 | 实际证据与结论 |
| --- | --- |
| 目录与原生 KB 边界 | 公开 GET tools 返回 native kb 的 5 项读方法和独立 management 的 9 项方法。catalog 请求链修复后管理组 active=true（主代理复核）。|
| MainChat 动态发现与创建 | 会话 `38e2b4c5-61d5-489c-b9b8-5809e86d44fa`，history `h_dm9ee42r82v4`；真实 search_tools/load_tools/knowledge.list 后并行调用 knowledge.create，创建 A `ds_09b10f333dd36b049f016609bfc22977` 和 B `ds_e07e060f944b454a7b8378536ac62bf4`，无确认卡。公开 MCP 按专用前缀查询独立核实两库存在。|
| 公开原始文件上传 | 179 字节验收文本上传成功，返回 file_ref、实际大小和 SHA-256；此步骤本身不代表内部工具导入成功。|
| CLI local_file 授权目录 | 使用本 worktree 编译的 CLI 和真实 Python MCP stdio client 完成 initialize 与 roots/list；roots 回调实际发生 1 次。授权目录中的 56 字节 fixture 上传 B 成功，文档 `doc_5b19f483dfd3ae736647e8c600e9853e`，processing submitted。|
| CLI 越界与无授权目录 | 同一真实 CLI：授权目录外路径返回 FORBIDDEN；不宣告 roots capability 返回 LOCAL_FILE_BRIDGE_REQUIRED，roots 调用为 0 次。两者均未导入文档。|
| 跨库移动拒绝 | history `h_dm9egoemlqf4`；真实 pending 卡包含资源名、完整路径、目标、1 文档、56 字节和影响说明。公开 decision rejected 后只读查询证明原文档仍在 B。历史持久化 ask_answered=true 与 __ask_user_no__。|
| 跨库移动批准 | history `h_dm9ej2hk0jxc`；第一次 preview 后目标库增加目录，批准后 claim 返回 resources changed，需要新的 prepare/确认。新卡获批准后文档移到 A，目标新 ID `doc_61fd4dfd94b5297bf5be287d3ef18dd4`，返回 old→new mapping、processing/cleanup submitted。|
| 目录创建 | 会话 `957ae5eb-5501-4dd7-9e7d-7baa3f7180ae`，history `h_dm9ejbzc2fmg`；创建 snapshot-root `doc_4574dca1580cd156b3b6ea578d5b713e` 和 cancel-root `doc_2d1461f1a9c3212211c99a4d901e50a0`。|
| 子树快照变化 | history `h_dm9ejtwymjc0` 中等待 snapshot-root 删除确认时，另一个真实 Chat（history `h_dm9ekgw4jamo`）创建子目录 `doc_7317e9b502aa92630dfee69cabbc17ca`。旧卡批准后执行被拒绝为 resources changed；Agent 重新准备新卡，本次拒绝。最终公开查询证明父、子目录都仍在。|
| 取消运行后旧确认失效 | 会话 `a6d4fca2-6f5e-4274-9b55-8ba0df68c8e6`；cancel-root 删除卡 pending 后，通过公开 stop 取消真实 run `run_dm9ekjhwun3s`。之后批准旧卡返回 HTTP409；run terminal 为 cancelled/user_cancelled；公开查询目录仍在。|
| 文档删除批准 | history `h_dm9ekxfuvk6w`，实际 confirmation approved 后删除 A 中移动得到的文档，返回 deleted 和 cleanup submitted。公开只读查询确认目标文档已不可见且 B 为空。物理 cleanup 完成尚未核验。|

## 已发现并修复的真实故障

1. Python `/api/chat/tools` 丢失 Core system_mcp_config，导致管理目录 inactive 或读取旧上下文。现在显式传入 request-local policy；ASGI 红→绿，catalog/registry 49 passed；已部署并复核。
2. Go JSON Schema 使用合法的 nullable type 数组，LazyLLM docstring hint 查字典时 TypeError，导致整个管理工具组不可用。修复仅影响说明生成，完整 schema 仍保留；红→绿，MCP 35 passed，真实目录 11 个知识工具都能转换。LazyLLM commit `f85451e8`，已部署并真实成功加载 9 管理工具。

## 仍未通过或未验收

- 首次内部 uploaded_file 导入：history `h_dm9efuydj0i8` / tool `tool_pJMy5RLUuClctYBBwg2XyMna` 返回结构化 INTERNAL，首次原因仍未明确。READY5 后主代理分别用真实新会话和同会话第二轮成功创建 `doc_79beaaccf1b886ef1341673e03241ab3` / `doc_ec67c510de8f7bf3631181de20972588`；成功复验不抹去首次未归因失败。
- 真实 Work：research task `d1af2986-e2df-4020-a198-a19152d98bd9` 经真实 create_subagent 创建，但首模型调用 HTTP400 `invalid_request_error`，无管理操作。Core→Python 的模型配置传递静态检查未见缺失，尚不能归因；主代理安排独立诊断。
- 主 Chat managed workspace local_file 已在 READY5 后真实通过：见后附补充证据。
- Writer / Image Work、固定 Workflow revision / PPT 兼容性目前仅有相关单元测试，未跑真实模型 E2E。
- UI 卡片最终视觉核验由主代理负责；本报告只确认实际事件和持久化数据。
- 成功批准后 run_finished 的 reason 仍是 awaiting_user_input，尽管操作已完成、ask_answered 已持久化。已报告主代理检查终态语义。

私有证据：create-chat-kimi.jsonl、created-fixtures-verification.json、cli-{allowed,outside,no-roots}.json、move-{reject,approve}-chat-kimi.jsonl、move-rejection-history.json、snapshot-{delete,change}-kimi.jsonl、cancel-delete-kimi.jsonl、delete-approve-chat-kimi.jsonl、after-confirmations.documents.json。确认 endpoint 的成功/失败响应各独立落盘。脚本 stdout 的 tools=[] 仅表示该 SSE 把工具标签放在 delta 文本而非 tool_calls 字段；不能据此判定没有工具调用，报告依据真实标签、确认记录与独立资源状态核验。

## READY5 补充：主 Chat local_file 与确认终态已真实通过

真实会话 `90f949c1-bde0-4154-8ee5-d15a07bf69ab`，首轮 history `h_dm9etwizs1jk` / run `run_dm9etwizs2bc`：

1. Agent 使用普通 write 在当前会话专用 runtime 工作目录创建 `KM-E2E-host-local.txt`，45 字节。实际 SSE 没有 ask_pending 或 workspace authorization 卡；未写入 checkout 或其他用户文件。
2. 通过动态 search/load 管理工具，真实调用 knowledge.document.upload，source.kind=local_file，path 使用 write 返回的实际绝对路径。Host 经已有文件授权/固定 fd 读取并传输原始字节，Core 创建文档 `doc_cf8d15f1518471c0810c7c4fb1ef5631`，processing=submitted；没有改用公开上传 API。
3. 独立通过公开、带真实 Bearer 的 `datasets/{dataset}/documents/{document}:download` 下载原件，与 write 产物逐字节比较完全相等：双方45字节、SHA-256均为 `57b4a4b1f577564fd005f4d593d115a6b937fa72f2a220db92ed24b9fa41efc9`。证据 `host-local-byte-verification.json`。
4. 同会话第二轮 history `h_dm9euqto89xs` / run `run_dm9euqto8apk` 仅请求删除上述新文档；产生真实业务确认卡，通过公开 bound decision approved 后执行成功。只读目录核验文档已经不可见；本地源文件按要求保留。
5. 实际 SSE 和重新读取的历史均为 `completed / normal`，历史 `ask_answered=true`、`ask_saved_answers[0]=__ask_user_yes__`。终态修复已部署并真实验证，之前的 awaiting_user_input 问题已不再复现。

证据：`host-local-upload-kimi.jsonl`、`host-local-byte-verification.json`、`delete-host-local-kimi.jsonl`、`after-hostlocal-delete.documents.json`、`host-local-final-verification.json`。当前无运行中的验收 Chat。

## Checkout 工具状态文件归因与隔离核验

代码目录出现的 3 份 JSON + 3 份锁文件不是生产 SubAgent 的配置泄漏。离线 `work400_probe.py` 在诊断 Kimi schema 时构造 Agent，首次沿用真实失败任务 d1af...，随后使用两个 work400-diagnostic UUID；当时只指定 task workspace_path，未设置 process-level LAZYMIND_AGENTIC_WORKSPACE。三个文件的 scope hash 分别精确匹配这些诊断上下文，时间也对应诊断执行（13:12–13:13），而原真实 Work 在12:59即失败。

诊断脚本现已在任何 lazymind 导入前设置私有 probe-agent-workspace。对三个 JSON/锁文件运行 lsof 确认无引用后，将共1191字节移到私有 `diagnostic-checkout-state/` 并保存 manifest，不删除或覆盖生产状态。checkout 不再出现 workspace 下的 Git 文件。没有改动生产业务代码。

独立计算当前真实 Research `45e6faf8-938e-447c-abba-da7e2282ef90`、Writer `d540facf-65a0-4512-ac20-72b6c9e23fdb`、Image `d881441d-3ff0-4b1d-9259-41111c254403` 的 user/conversation/task scope hash：三者对应 JSON 均存在于 `/Users/dengyuang/workspace/lazymind-runtime-8091/data/homes/lazymind/agent_workspace/tool-retrieval-state`，均不存在于 checkout。此检查证明状态目录隔离；其业务功能是否完成仍以 runtime_review 的独立 E2E 报告为准。
