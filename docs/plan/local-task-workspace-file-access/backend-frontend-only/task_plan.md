# 剩余任务与验收清单

唯一方案入口：IMPLEMENTATION_PLAN.md。算法不能修改；Local/Desktop 已冻结。当前仅文档维护，不代表下列测试/实现获批。

## 已完成的本轮整理

- [x] 核对 bb46abd6 与旧需求，保留当前审计证据。
- [x] 固定算法零改动，删除申请算法例外的路线。
- [x] 清理已完成基础实施步骤、旧工作树路径及过期 MCP/轮后落盘草案。
- [x] 将前后端可修复项 U/C 与未解决文件能力 F 分开维护。

## 前后端补齐任务

- [x] T1 阶段一：已为 U1–U3 写真实组件回归。13 项中 7 项稳定复现已有任务换绑、会话切换残留、异步迟到和 disabled/运行中权限问题，6 项确认取消/关闭/Esc 及既有隔离行为；生产代码净增为 0，并已完成人工 Review。
- [x] T2：已在现有控件修复状态/绑定/统一会话代次校验与禁用条件，并补充权限确认、权限更新、撤销迟到和同草稿列表保留 4 项合同；组件 17/17、聚焦矩阵 39/39 通过。
- [x] T3：已复用已有 API 补搜索、失效历史项重授权、授权管理、错误本地化和冲突刷新，覆盖 U4–U5。
- [x] T4：已校验 AskCard 问题/答案结构，并按官方 runner 读取顺序统一顶层、附件和 parent 身份；保留无 questions 的旧历史兼容。
- [x] T5 自动化集成：发送/预览、权限保存、撤销、重授权、AskCard 透传和子任务创建/恢复链路通过现有及新增回归；真实 Local/打包 Desktop 验收留在 T6。
- [ ] T6：分别记录 Local/打包 Desktop UI 验收、必要的 PostgreSQL/SQLite 元数据测试及实际代码规模。

## 前端稳定性补齐（2026-09-09 已批准）

- [x] S1：测试先行，复现 Local Proxy 大写错误码退化为 unknown，以及授权管理查询在会话切换后继续显示；新增 6 项预期失败、原有 23 项通过、异常失败 0。
- [x] S2：最小修改现有控件、错误 helper 和中英文 locale；生产代码净增 11 行，S1 聚焦矩阵 29/29 通过。
- [x] S3：前端聚焦矩阵 49/49，ESLint、TypeScript、生产构建和冻结边界检查通过；四份文档已同步，生产代码净增 11 行。

本批不执行 T6，也不实现或测试 Agent 对本机工作区文件的读、新建、修改、追加、删除。

### S1–S3 执行步骤

**文件职责**

- 测试 `frontend/src/modules/chat/utils/localWorkspace.test.ts`：验证 Local Proxy 错误码归一化。
- 测试 `frontend/src/modules/chat/components/ChatInput/LocalWorkspaceControl.test.tsx`：验证会话切换关闭管理窗口并隔离在途查询。
- 生产 `frontend/src/modules/chat/utils/localWorkspace.ts`：继续作为唯一错误 reason 解析入口。
- 生产 `frontend/src/modules/chat/components/ChatInput/LocalWorkspaceControl.tsx`：复用现有请求序号处理管理窗口生命周期。
- 生产 `frontend/src/i18n/locales/zh-CN.ts`、`frontend/src/i18n/locales/en-US.ts`：只增加选择禁止和选择过期文案。

**测试先行**

- [x] 在 utility 测试加入表驱动断言：`LOCAL_WORKSPACE_SELECTION_EXPIRED` → `selection_expired`、`LOCAL_WORKSPACE_SELECTION_FORBIDDEN` → `selection_forbidden`、现有 invalid/path/mode 大写码映射到已有 reason。
- [x] 在组件测试用可控 Promise 启动管理列表请求，随后从草稿 rerender 到已有任务；断言 Modal 进入关闭状态，迟到结果不显示旧路径。
- [x] 运行 `NODE_OPTIONS=--no-experimental-webstorage pnpm exec vitest run src/modules/chat/utils/localWorkspace.test.ts src/modules/chat/components/ChatInput/LocalWorkspaceControl.test.tsx`，结果为新增 6 项预期失败、原有 23 项通过、异常失败 0。

**最小实现**

- [x] `workspaceReason` 按 Core 嵌套 reason、Local Proxy `response.data.code`、错误对象顶层 code 的顺序取字符串，并通过固定 map 归一化已有主机错误码；非字符串返回 `unknown`。
- [x] 会话 effect 开始时递增 `listRequestRef`、关闭 `manageOpen`、清空 `managedItems`，使旧查询不能更新新会话界面。
- [x] 两份 locale 增加 `selection_forbidden` 和 `selection_expired`，不改其他产品文案。
- [x] 重跑 S1 聚焦命令，29/29 通过。

**回归与交付**

- [x] 运行六文件前端聚焦矩阵、相关 ESLint、`pnpm exec tsc -p tsconfig.mcp.json --noEmit` 和 `pnpm run build`，全部通过。
- [x] 核对 `algorithm/`、`tests/algorithm/`、LazyLLM gitlink 与 `245bc26d` 一致，Local/Desktop 与 `ec4676e0` 一致；Backend 和范围外 diff 均为空。
- [x] 统计生产 diff并更新本目录四份文档；本批明确不宣称 T6 或 F 类完成。

T1–T6 依赖当前已实现的 grant/binding/bridge。不得再次提取 Local/Desktop 补丁或重做迁移。权限/API 相关测试与实现遵循人工 Review 门禁。

## 文件能力研究与准入

- [ ] F-R1：官方既有同步工具入口能否完成同一 run 的 mkdir/create/read/overwrite/append/replace/read，并返回真实结果。
- [ ] F-R2：不改算法时，证明工作区身份/授权/权限版本在操作和提交阶段可被强制校验。
- [ ] F-R3：证明批准绑定原调用/参数且单次消费，拒绝/超时/撤销不执行，不靠 ask_user 新轮次模拟同步暂停。
- [ ] F-R4：证明既有 local_fs、命令、联网/应用以及主子任务不会绕开控制；Workflow 保持原有边界。
- [ ] F-R5：说明并验证凭据不进模型、缓存有界、重试不重复追加、异常结果不虚报成功。
- [ ] F-R6：只有上述项通过，才写正式前后端实现合同及代码范围；未通过继续记录未解决，不请求算法例外。

## 最终可观察验收

- [x] 组件合同：已绑定任务不换绑；无绑定已有任务不新增绑定；切会话及查询、picker、authorize、权限、撤销迟到回调不串目录。真实 Local/打包 Desktop 验收仍归 T6。
- [x] 自动化合同：搜索传递名称/路径 query 并包含失效项；失效项可重新授权且新 grant 不绑定旧任务。真实 UI 验收留在 T6。
- [x] 组件合同：取消/关闭/Esc 不修改选择；禁用覆盖草稿目录入口；运行中后续权限修改调用现有 next_request API。真实运行态验收仍归 T5/T6。
- [x] 自动化合同：版本冲突刷新状态；错误 reason 有中英文；AskCard 与附件/任务身份参数保持兼容。真实运行验收留在 T6。
- [ ] 若文件能力准入通过：同轮读建改、版本冲突、敏感/.git 边界、提交前撤销、原删除命令、子任务批准分别有真实证据。
- [x] 算法相对官方基线零差异，Local/Desktop 相对冻结提交零差异；无越界文件、依赖或新工作树。
- [x] 已报告每批实际规模与复用点，并区分组件自动化、静态检查和未执行的人工/端到端验证。
- [x] 当前仅报告前端管理链路缺陷已补齐；F 未完成，不报告旧版完全复现。
