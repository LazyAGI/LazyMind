StateGraph 可视化帮助理解工作流状态；StateGraph 编辑器（P1，仅限管理员）支持热重载修改状态机；

skill -> 
前端可视化插件
拖拽 + 连线修改，在线编辑各阶段信息和状态转移方程
校验

## 来自 M7 的补充目标（StateGraph 编辑器 P1）

- 权限控制：仅 admin 角色或 LAZYMIND_DEV_MODE=true 时可访问编辑器
- GET /api/v1/plugins/:plugin_id/state-machine：返回当前 state.yml 文本内容 + 解析后图结构 + 节点布局（layout）
- PUT /api/v1/plugins/:plugin_id/state-machine：保存修改后的状态机；先调 Python validate_state_yml() 校验，有错误返回 422 + errors 列表，无错误写入 state.yml 文件 + 触发 loader.reload_plugin() 热重载；节点布局持久化
- StateGraph editable 模式：节点/边增删改（步骤 ID、label、default_mode、condition 内联编辑）、节点拖拽（layout 持久化）、YAML 预览面板（图形编辑时实时生成 YAML，也支持直接编辑 YAML）、保存时展示 422 错误不关闭编辑器
- loader.py 新增 reload_plugin() 方法：重新解析 state.yml 并更新 StateMachine 缓存（不重启服务）；用读写锁保护缓存防止热重载期间并发竞争
- state.yml 存储位置：容器化部署中需考虑将 state.yml 存入 DB（BLOB）而非仅依赖文件系统