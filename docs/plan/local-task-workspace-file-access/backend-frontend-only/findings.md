# 当前差异证据

## 本次接手源码复核（69d4b603）

- `LocalWorkspaceControl.tsx` 在有 `conversationId` 时仍保留可点击的目录按钮；Core 的 `ensureConversationWithWorkspace` 会对新增或不同 binding 返回 `binding_locked`，因此 U1 是前端状态约束缺口，不需要修改 Core 绑定规则。
- 组件加载 effect 只用局部 `active` 防止旧绑定查询回写，但不会在 `conversationId` 变化时立即清空 `selected/items` 或通知父组件；`choose` 和 `allow` 的异步结果也不受该 `active` 标记保护。
- 目录按钮接收 `disabled`，最近目录 Select 没有接收；权限 Select 使用同一个 `disabled`，与 `next_request` 权限更新语义冲突。
- `listWorkspaces` 尚未暴露 Core 已实现的 `query/include_inactive` 参数；Core 的 `InternalPrepareReauthorization`、权限版本冲突和结构化 reason 均已存在，可直接复用，无需新增服务、依赖或数据库表。
- `validateWorkspaceAskSubmission` 仅比对最近未回答卡片的 `ask_id`；`RebuildSubagentParams` 仅写入 `parent_agentic_config.user_id`，而官方 `runner.py` 构建 agentic config 时依次读取 `attachment_context.user_id`、顶层 `params.user_id`、parent 值。C1/C2 的文档判断与当前源码一致。
- 当前 `LocalWorkspace.contract.test.ts` 的三项检查均读取源码并匹配字符串，不能覆盖 U1–U3 的交互和异步竞态；第一批应使用 React Testing Library 的 render/rerender/fireEvent 与可控 Promise 建立失败合同。

## T1 阶段一测试证据

- 新增 `LocalWorkspaceControl.test.tsx`，直接渲染真实组件和 Ant Design 控件；只 mock 运行模式、HTTP/bridge 边界、消息提示，使用可控 Promise 重现竞态。
- 单文件运行结果为 13 项中 7 项预期失败、6 项通过。预期失败准确命中：绑定任务目录按钮未锁定、已有未绑定任务仍可选择、切会话残留旧状态、旧 picker 结果显示授权框、旧 authorize 结果回写、disabled 草稿 Select 可用、运行中权限 Select 不可用。
- 通过项确认：旧绑定查询的 `active` 防护有效；原生 picker 取消、Modal Cancel、关闭按钮和 Esc 均不调用授权或 `onChange`；确认授权时只传 runtime 和 selection token。
- 初次增加 Modal 关闭测试时，jsdom 不执行 CSS leave 动画，导致以“节点移除”为标准的三项异常失败；测试改为检查 Modal 已进入 leave 状态，并将 Esc 发到实际键盘容器。调整后异常失败归零。
- 最终聚焦矩阵共 30 项：7 项预期失败、23 项通过；现有三个 workspace/bridge 文件的 17 项在加入新文件前后均保持通过。新测试 ESLint、diff 检查及冻结边界检查通过。阶段一不把预期 RED 计为产品验证通过。

## T2 实现与验证证据

- 仅修改既有 `LocalWorkspaceControl.tsx`：会话变化时立即清空目录、候选、权限和父状态；复用 request-id 模式为查询、picker、authorize、权限和撤销建立统一代次校验。
- 已有任务目录入口锁定：有 `conversationId` 时，已绑定任务不能换绑，未绑定任务不能新增绑定；失效绑定仍保留后续重授权入口。Core 的 binding 权威规则未改变。
- `disabled` 只禁用草稿目录选择入口；已绑定任务的权限 Select 保持可用，继续调用现有 `updateWorkspacePermission` 并沿用 `next_request` 语义。
- 对静态风险确认框捕获打开时的会话代次；切会话后确认旧 allow-all 不再发请求。权限更新和撤销已经提交到后端时不伪造取消，只丢弃不属于当前会话的返回值和消息。
- 阶段二新增 4 项合同后，组件测试 17/17 通过；其中额外确认取消 picker 不会作废同一草稿仍在加载的最近目录列表。连同 ChatInput 装配、workspace utility 和 Desktop bridge 的聚焦矩阵为 39/39。ESLint、`tsc -p tsconfig.mcp.json --noEmit` 和 `pnpm run build` 通过。
- 本机 Node 26 默认暴露实验性 `localStorage`，使 `ChatInput/index.test.tsx` 收集前失败；使用 `NODE_OPTIONS=--no-experimental-webstorage` 后通过。发布工作流为 Node 20，本批未修改测试基础设施。

## T3–T5 拉通证据

- `listWorkspaces` 现直接传递 Core 已支持的 `query/include_inactive`；管理 Modal 展示 active/revoked/path_unavailable，复用同一 token 重授权流程和撤销 API。
- 权限、授权和撤销遇到 binding_conflict/workspace_not_found/revoked/path_unavailable 时重新读取 Core 状态，不保留失败的乐观值；reason 使用中英文固定字典，未知值有明确兜底。
- AskCard 新记录逐题校验问题文本、类型、原 choices、自定义 choices 数量及答案值类型；null 答案仍表示省略。无 questions 的旧历史仅按 ask_id 校验以保持兼容。
- 官方 runner 优先读取 `attachment_context.user_id`；Core 现在对真实绑定同时归一化顶层、附件上下文和 parent 的 user/conversation，且保留附件内其他字段。
- 自动化结果为前端 43/43、Core 三包通过、静态检查与生产构建通过；这不是打包 Desktop 的人工 UI 证据，也不证明 F 类文件执行闭环。

## 2026-09-09 后续稳定性审计

- Local Proxy 的 workspace 端点用 `{code: "LOCAL_WORKSPACE_SELECTION_EXPIRED"}` 等响应错误；Axios 将其放在 `error.response.data.code`。当前 `workspaceReason` 只读取 Core 的嵌套 reason 和错误对象顶层 `code`，因此主机选择过期、选择禁止等错误会落入 `unknown`。
- 当前中英文 reason 字典没有选择过期和选择禁止文案。已有 mode/path/invalid 文案可以在归一化后继续复用，不需要修改 Local Proxy 或 Core 协议。
- `loadManagedItems` 只用独立的 `listRequestRef` 处理查询先后顺序。会话改变仅递增 `requestRef`，不会关闭 `manageOpen`、清空 `managedItems` 或作废在途管理查询；草稿切换到已有任务时，管理窗口及旧授权结果仍可能显示。
- 修复可限制在 4 个既有前端生产文件，预计净增 30–50 行。算法、Backend、Local/Desktop 均无需修改；最终人工验收和文件执行能力不属于本批。
- S1 首次运行时管理窗口用例被前序 Ant Design confirm 的离场动画节点干扰，`findByRole("dialog")` 因多个匹配而异常失败。改为按 `chat.workspace.manageTitle` 定位所属 dialog 后，结果稳定为 5 个错误码断言收到 `unknown`、管理 dialog 未进入 leave 状态；这是 6 项预期产品失败，不再包含测试设施异常。
- S2 以固定 map 归一化 5 个现有 Local Proxy code，保持 Core detail reason 的读取优先级，并对非字符串 code 返回 unknown；没有把任意大写文本动态转换为翻译键。会话 effect 递增既有 `listRequestRef` 并清理管理窗口状态，迟到列表因此不能回写。

## 基线

审计提交 bb46abd64ca5fc431f4f7748fb9e085099990d5e，旧对照 e7ed8a4189bb627e96814fc2f34818693cbc2050。旧 spec/checklist/代码在 Git 历史中可查；不继承其勾选为当前验收结果。

当前算法相对 245bc26d 零差异，Local/Desktop 相对 ec4676e0 冻结提交零差异。后续算法不允许修改，相关例外方案已经移除。

## 本机实际检查（2026-09-08）

| 检查 | 结果与限制 |
|---|---|
| backend/core：go test ./localworkspace ./chat ./subagent -run 'Test.*(Workspace\|LocalFS)' -count=1 | 3 包通过 |
| 前端现有 workspace/bridge 聚焦测试 | Node 24.19.0 下 3 文件 17 项通过；不等于完整 UI 验收 |
| 默认 Node 20.20.2 运行相同 Vitest | jsdom/undici 报 webidl.util.markAsUncloneable 不存在，收集前失败；换已有 Node 24 后通过，未改依赖 |
| 官方文件工具临时探针 | 实际导入当前 LocalFileToolkit/write_file，没有替换工具方法；所有文件为临时假数据 |
| 前端组件临时探针 | 实际组件+React/testing-library/jsdom，API/展示组件为替身；不冒充真实浏览器 E2E |

前端测试文件：src/modules/chat/components/ChatInput/LocalWorkspace.contract.test.ts、src/modules/chat/utils/localWorkspace.test.ts、src/runtime/desktopBridge.test.ts。LocalWorkspace.contract.test.ts 的 3 项为源码字符串断言，不能覆盖交互缺陷。

## 文件工具级复现

临时脚本 /tmp/lazymind-parity-review.fOKiMM/probe.py 使用已有 local/build/deps/python/algorithm/bin/python；配置、日志和 fixture 均在同一临时目录，未修改算法。

| 操作 | 当前实际结果 |
|---|---|
| 检查工具列表 | ls/glob/grep/read/string_replace/info；trusted=false |
| 从非工作区 cwd 读取 existing.txt | 相对路径失败；绝对路径成功、返回绝对路径且无 version |
| write_file 创建/追加宿主文件 | 两者均 ToolExecutionError |
| 配置 always_ask 后直接 string_replace | 文件修改成功；说明工具层无该批准检查，不表示模型一定忽略提示词 |
| read 后外部增加一行，再 replace | 接受修改；接口无 expected_version |
| service-account-fixture.json（只有 marker 假数据） | read/replace 均成功；旧规则要求敏感读批准、敏感写禁止 |
| .git/fixture.py（临时假文件） | replace 成功；旧规则禁止 .git 内写入 |
| 同一工具上下文中将根移走、原路径改为指向 sibling 临时目录的链接 | read 读取替代目录；没有复核旧授权的文件系统身份 |

可迁移的复现方法：在临时 root 创建 existing.txt=alpha；设置 agentic_config.local_fs_sources 为 source_id、paths=[root]、file_extensions=[txt,json,py]，并设置 workspace_permission_mode=always_ask。直接调用实际 read/string_replace 并断言磁盘；用 sibling 临时目录测试根替换。不要使用真实敏感文件或用户工作目录。

这些证据不是撤销端到端测试；Core 的取消通知仍存在，但不能由通知存在推导出逐次访问/提交校验已经恢复。

## 组件级复现

临时脚本 /tmp/lazymind-parity-review.fOKiMM/ui-probe.cjs 转译实际 LocalWorkspaceControl.tsx，未替换其状态和事件逻辑。

1. 已绑定 conv-a/grant-a 的目录按钮仍可用；选择并确认 grant-b 后 onChange 实际发出 grant-b。Core 绑定锁会拒绝随后的请求。
2. 同一组件从 conv-a rerender 到无绑定会话，旧 Alpha 仍显示，onChange 没有发出清空值。
3. 新草稿 disabled=true 时，最近目录 Select 仍可用。

正式回归需用 render/rerender/fireEvent 和可控 Promise 覆盖同样步骤，以及 picker/authorize 迟到、取消、历史重授权、运行中权限和错误反馈。临时脚本不是远端测试交付，其他机器按上述步骤重新构造 fixture。

## 源码证据落点

- backend/core/localworkspace/context.go：sources 及 ModelNotice，权限表达为文本。
- algorithm/lazymind/chat/engine/tools/local_fs.py：实际工具列表、路径解析、read/string_replace。
- algorithm/lazymind/chat/service/chat_service.py：MCP 只在非 Workflow 主轮加载；缓存 key 包含完整 server 配置。
- algorithm/lazymind/chat/engine/subagent/runner.py：工具从 DEFAULT_TOOLS/Workflow package 解析；身份优先读取 attachment_context/顶层 params。
- backend/core/localworkspace/subagent_context.go：当前只重建 parent 等字段，需要核对实际消费者优先级。
- frontend/src/modules/chat/components/ChatInput/LocalWorkspaceControl.tsx：绑定状态/异步回调/Select 缺陷。
- frontend/src/modules/chat/utils/localWorkspace.ts：列表未带 include_inactive/search，reason 未完成本地化映射。

未验证：真实模型同轮完整文件闭环、Local/打包 Desktop 全 UI、真实子任务批准、Windows 文件操作。不得以已有测试通过替代这些验收。
