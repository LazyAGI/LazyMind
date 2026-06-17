1. 用户说，我对xx不满意，帮我重新跑阶段1-3，然后给我确认  （难点，目前的auto是全局的，不受agent控制，此举需要agent分析用户意图，决策下一步是否auto，甚至要直接让ChatAgent连续推进多个SubAgent的流程，而不是像之前一样，一次只推进一个step）；如果一次性推进多个step，中间是否需要DriverAgent参与呢，是否需要go参与，还是直接走sync模式处理掉
2. 没有依赖的step并行执行
3. async job和定时任务
4. 查询指令

## 来自 M3 的补充目标

5. human 模式：步骤完成后 step_status 置为 waiting，AsyncJob Handler 通过 Redis BLPOP 阻塞等待用户确认信号（key: plugin:proceed:{session_id}），lock_ttl_seconds=86400（支持长时间等待）
6. POST /plugin-sessions/:id/proceed 接口：human 模式下用户点「继续」触发，写 Redis 信号唤醒 Handler；仅当 step_status='waiting' 时有效，否则返回 409
7. plugin_proceed 工具（Python）：Agent 调用，含 state.yml 硬约束校验（非法 transition 直接拦截，不写 Redis）；state.yml 缺失时回退到 plugin.yaml steps 线性顺序
8. plugin_edit 工具（Python）：对当前步骤 artifact 发起修改请求，写 Redis 信号触发 Handler 重新执行修改逻辑；框架记录每步 edit 次数，超出 driver.md 规定上限时强制推进
9. auto 模式 driver 触发：步骤完成且 step_mode='auto' 时，框架自动构造 system prompt（注入 driver.md + 当前 artifacts 摘要），发起一次 ReactAgent 调用；driver.md 缺失时降级为 human 模式并打印 warning
10. step_change SSE 事件：统一字段（plugin_session_id / step / step_status / step_mode），Go 接收后写 plugin_session_steps 表并更新 current_step_id FK
11. plugin_sessions.current_step_id 改为 FK 指向 plugin_session_steps.id（M3 正式约束）
12. async_jobs 新增 conversation_id 和 lock_ttl_seconds 字段
13. StepProgress 前端组件：展示步骤列表 + 状态（running 旋转/waiting 等待图标/done 勾选）、human/auto 切换 Toggle（调 PATCH 接口）、waiting+human 时显示「继续」按钮
14. plugin_proceed 并发幂等：用户 UI 点击和 Agent 同时触发时，Handler 侧做幂等处理，避免重复执行