提供用户监控能力的增强功能：任务看板提供会话级 Job 监控。

看板，监控；
前端页面手动新建定时任务

## 来自 M7 的补充目标

### 任务看板（P0）

- GET /api/v1/conversations/:id/jobs 接口：返回当前会话下所有关联 Job 及其 plugin session 信息（plugin_id / plugin_name / current_step / step_status / step_mode / steps 列表 / job_status）
- TaskBoard 前端组件：展示所有 plugin session 进度条；每个任务卡片含插件名、当前步骤、步骤状态；点击跳转到对应 PluginShell；监听 step_change SSE 实时更新

### StateGraph 可视化（P0）

- GET /api/v1/plugin-sessions/:id/state-graph 接口：合并 state.yml 图结构 + DB 运行态，响应含 nodes（id/label/status/step_mode/artifact_summary）、edges（from/to/condition/is_valid_from_current）、current_step
- StateGraph 前端组件（readonly）：基于 ReactFlow，节点展示步骤名 + 状态 badge，边展示 condition label（悬停全文），合法后继边虚线高亮，节点展开显示 artifact_summary
- 集成到 PluginShell 侧栏「流程图」Tab（懒加载 ReactFlow 包以控制体积）
- 监听 step_change SSE，步骤状态变化时自动刷新图中节点 badge