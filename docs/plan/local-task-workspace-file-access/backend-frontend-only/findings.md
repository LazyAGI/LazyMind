# 当前差异证据

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
