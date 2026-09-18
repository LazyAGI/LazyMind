# 工作流状态管理与实时同步

## 状态归属

Workflow Runtime 的执行记录与图投影是工作流状态的依据。前端不从聊天文本、
工具输出中的“已完成”，或子任务数量推断整个工作流完成。

| 数据 | 作用 |
| --- | --- |
| `plugin_session_steps` | 每次执行的状态与有效性；重试产生新 attempt，旧记录标为 stale |
| `plugin_sessions` | 会话状态、当前步骤、状态版本和固定的工作流版本 |
| 图投影 `projection` | 当前步骤、可执行步骤、分支、审批属性和整个流程是否完成 |
| `sub_agent_tasks` | 子任务日志、进度和产出；展示状态与相同 task_id 的工作流执行记录对齐 |
| `workflow_events` | 持久事件；跨进程写入和内存通知遗漏都能通过此日志恢复 |

“子任务成功”与“工作流完成”不是同一个状态。整个流程完成要求图的完成条件成立，
不能只检查最后一个工具是否成功，也不能按固定的 UI 标签数量计数。

## 前端消费流程

1. 首次读取最新会话，随后读取 `/workflow-sessions/{id}/projection`。
2. projection 接口在同一个数据库事务中读取状态、版本、图投影与 attempt_history。
   面板步骤列表由该 attempt_history 生成，不再与独立的步骤请求拼接。
3. `useWorkflowSession` 在挂载后订阅工作流 SSE；同一 session 的订阅按引用计数复用。
4. `workflowPanel` store 按版本接收快照。旧 REST 响应、旧快照不能覆盖新状态；
   请求期间若收到事件且无法证明响应更新，则保留实时状态并补一次查询。
   接受 REST 快照时也同步推进 SSE reducer 缓存，避免后续进度事件恢复旧图状态。
5. 右侧工作流子任务与主面板使用同一份 attempt 状态；非工作流子任务保持原有逻辑。
   历史失败仍可在执行历史中查看，但不能参与当前流程阻塞或恢复提示。

`autoRunning` 只表示自动调度阶段的临时活动。终态快照会清除它，延迟到达的
Driver 活动不能把已完成或失败的工作流改成执行中。真正重试、回退后，以更新版本
的运行快照重新打开会话。

## 事件与版本约定

- `cursor` 是 `workflow_events.id`，在整个数据库中自增。不同会话会交错写入，
  因此单个会话的 cursor **不保证连续**；不能把数字跳跃直接判定为丢事件。
- `state_version` 是工作流状态版本。批量启动由 transition 一次预留版本，
  每个子步骤不能重复增加批次版本。执行事件携带所属会话的当前版本。
- 旧 executor 的版本 0 attempt/step 事件可更新实体记录，但不能回退工作流版本。
  被判定过期的事件也必须消费 cursor，避免反复重连。
- 状态变化与状态事件在同一事务提交；路由收敛时写入完整的 `workflow.snapshot`，
  包括完成状态与执行历史。
- SSE 同时使用内存通知和默认每秒一次的增量日志读取。内存通知只负责唤醒，
  实际总是按数据库 cursor 顺序读取，避免跨进程写入或通知队列溢出造成漏更新。
- 图投影可能因一次操作增加多个版本；版本号也不要求每条事件恰好增加 1。

## 状态与等待文案

`projection.current` 包含失败、中断、取消的执行，**非空不等于正在执行**。
应检查对应 node 的 execution：

| 条件 | 展示 |
| --- | --- |
| 有 pending / queued / claimed / running 的有效执行 | 执行中 |
| 图投影 completed 为 true | 已完成 |
| 当前执行失败 | 失败 |
| 实际存在成功后待审批的节点 | 等待审批 |
| 等待所需输入材料 | 等待输入 |
| 无执行记录且第一步已就绪 | 待执行 |
| 其他暂停、等待下一步调度或用户继续 | 待继续 |

缺模型配置通过当前有效失败对应的配置卡展示，不能把普通 waiting 全部叫作审批。
自动分析之后的配置检查使用保存的结果和路由断点；用户配置后继续只重试检查，
通过后沿原来的下一步继续，不重新调用分析模型。

### 继续按钮与自动调度间隙

`waiting` 本身不代表需要用户点击继续。自动步骤结束到下一步派发之间，也可能短暂
进入 waiting。审批设置已随首次 projection 返回，且包含用户的跳过审批偏好，
前端直接消费 `nodes[step_id].requires_approval`，不需要分析模型重复判断。

审批发生在该步骤执行完成后：只有最新有效步骤成功且需要审批、同时没有其他执行
处于 pending / queued / claimed / running 时，才显示审批操作。不能因为下一步
将来需要审批，就在它执行之前显示继续。自动步骤间隙和首次自动派发前不显示继续。

明确中断的执行保留“恢复执行”；工作流声明的完成后追加操作仍可使用。点击操作和
异步保存结束后，都会复核当前 session、状态版本、步骤与操作类型，防止页面更新或
保存期间工作流已前进，却又发出旧的继续指令。

## 延迟事件与提示失效

延迟的 waiting 回调不能覆盖 completed / failed / stopped 会话，也不能覆盖已有
pending / queued / claimed / running 有效执行的会话。检查在数据库更新条件中完成，
不能仅在更新前单独查询一次，以免查询后新步骤启动。

配置卡仅对应仍有效的失败 attempt。发生以下情况时清除卡片及 pending sessionStorage：

- 工作流已完成或停止；
- 原失败记录变为 stale；
- 同一步已有更新的执行，或已经恢复成功。

这样通过工作流按钮、聊天指令等其他入口恢复后，也不会重新出现旧“配置／继续”卡。

## 回归检查

后端：

```sh
cd backend/core
go test ./workflow/... -count=1
```

前端主要回归：

```sh
cd frontend
pnpm exec vitest run \
  src/modules/chat/store/workflowStateSync.test.ts \
  src/modules/chat/store/workflowStatus.test.ts \
  src/modules/chat/store/taskCenter.test.ts \
  src/modules/chat/components/TaskCenter/index.test.tsx \
  src/modules/chat/components/newChatContainer/hooks/useChatConversation.test.ts
```

应持续覆盖：运行时旧 waiting、完成后延迟响应、合法重试/回退、并行 queued/claimed
步骤、全局 cursor 跳跃、版本 0 旧事件、无内存通知的持久完成事件，以及旧配置卡清理。
排查时先对照同一 session/task 的数据库状态、projection 版本、事件 cursor，再看页面；
不要把另一轮 attempt 或历史工具文本当成当前执行状态。

CI 另有独立的前端契约测试目录，修改同步机制时也必须运行：

```sh
cd tests/frontend
npm test
```

其中 `workflowProjection.test.js` 检查事件版本与重放规则，
`workflowEventStreamSurface.test.js` 检查订阅、引用清理与会话刷新入口。
它们与 `frontend/src` 下的测试共同维护同一份状态契约。
