# 阶段 4：驱动模式增强与并发能力

> 在已实现的 auto/dynamic/never 驱动模式基础上，增强驱动模式的灵活性：支持用户对步骤结果的局部修改、无依赖步骤的并行执行、多步骤连续推进、异步定时任务，以及自然语言查询插件状态。涵盖插件/SubAgent 驱动模式的精细化控制、用户意图与约束的识别与维护，以及 DriverAgent 能力补全。

---

## 一、驱动模式控制

1. **Plugin 三级模式**：插件驱动模式分为三档，`default=dynamic`：
   - `auto`：每次 `advance_step` 后 ChatAgent 退出，由 DriverAgent 负责裁决是否推进下一步。
   - `dynamic`：ChatAgent 自行决定本轮推进步数，不启用 DriverAgent；默认每次推进1步后等待用户，除非用户明确指定范围（如「重跑1-3」）或提示词另有说明；不退出会话直到 ChatAgent 决策停止。
   - `never`：关闭插件机制，适用于纯问答场景。
2. **SubAgent 两级模式**：控制大模型自主发散型 SubAgent（与插件步骤无关），`default=auto`：
   - `auto`：ChatAgent 可自主创建 SubAgent 推进发散目标，由 ChatAgent 直接驱动，不经过 DriverAgent。
   - `never`：禁用大模型自主 SubAgent，不影响插件步骤的 SubAgent 执行。

## 二、用户意图与约束管理

3. **意图与约束识别**：ChatAgent 在每轮对话中持续识别用户给出的意图和约束项；约束项分为全局约束（适用于整个插件执行流程）和步骤级约束（仅限定某个步骤），整理后在前端步骤状态旁边附注展示。
4. **意图与约束变更修订**：当用户在对话中更新意图或约束时，ChatAgent 及时识别变更，重新修订全局意图及后续未执行步骤的执行参数；已完成的步骤不重新执行。
5. **缺失意图问询**：ChatAgent **始终**具备 `ask_user` 工具。`dynamic` 与 `auto` 在**插件执行前**均可主动 ask；**插件执行中** `dynamic` 可 ask，`auto` 不主动 ask、应合理推断，用户显式要求确认时除外（详见 plan §8.4）。

## 三、步骤级执行控制

6. **`dynamic` 步骤等待**：`dynamic` 模式下步骤 SubAgent 完成后进入 `step_waiting`（`reason: dynamic_pause`），暂停自动推进；前端显示「继续」/「重试」按钮，等待用户下轮消息或按钮模拟消息后再推进。
7. **手动推进接口**：用户点击「继续」时，通过标准对话消息通道发送确认（复用已有 `POST /conversations:chat`），由 ChatAgent 解析意图并调用 `advance_step`；防止用户重复点击导致重复触发。
8. **用户主动停止**：用户在执行过程中点击「停止」时，立即中止当前步骤；当前步骤标记为停止态（优先复用现有终止状态），后续步骤不继续推进，等待用户下一步指令。
8a. **中断恢复（Checkpoint-Resume）**：步骤被中断（用户停止或异常）但已有部分 artifact 保存时，用户说「继续」应走增量模式——SubAgent 识别已完成的 output_key，仅生成缺失部分；用户说「重试」则全量重跑忽略 checkpoint。ChatAgent 根据中断状态和已有 artifact 覆盖率自动决策走 resume 还是 retry。
9. **用户指定范围重跑**：用户说「重新跑阶段1到3」时，ChatAgent 能解析出目标步骤范围，决策是否需要 DriverAgent 参与，连续推进多个步骤，完成后给用户一次汇总确认，而非每步都中断等待。

## 四、步骤结果修改

10. **局部修改请求**：用户可以在步骤完成后要求对已产出的 artifact 进行局部修改，而不是全量重跑整个步骤；ChatAgent 识别修改意图后，向 SubAgent 传入修改指令，SubAgent 仅针对被指定的内容部分重新生成。
11. **修改次数限制**：仅在 auto 模式下生效；框架记录每个步骤的 DriverAgent RETRY 次数，超出**全局**上限（默认 3，见 plan §11.3，**非** `plugin.yaml` 配置）后强制 PASS，避免无限循环。dynamic 模式下不限制。

## 五、并行执行

12. **无依赖步骤并行**：当同一插件中存在多个互不依赖的步骤时（根据 state.yml 的 inputs 声明判断），支持同时启动多个 SubAgent 并行执行；所有并行步骤完成后再进入下一个依赖它们的步骤。
13. **并行状态感知**：前端 Plugin Panel 和 StateGraph 能正确展示多个步骤同时处于 running 状态的情况；DriverAgent 在并行步骤全部完成后才做整体裁决。

## 六、异步任务与定时触发

14. **异步 Job 支持**：插件步骤支持以异步 Job 形式提交执行，用户不必保持连接等待；Job 完成后通过 Conversation Events SSE 或通知机制告知用户结果。
15. **定时触发**：用户在对话中通过 ChatAgent 创建**用户级**定时规则（`user_schedules`，非 `plugin.yaml`）；每次到期在 `task_center_tasks` 产生一次执行实例（`schedule_id` 关联规则），与普通 Task 同列表展示。

## 七、DriverAgent 能力补全

16. **失败降级策略**：DriverAgent 执行失败时，默认 fallback 到 dynamic 模式（让用户通过对话介入），而非静默 pass 继续推进，避免在错误状态下盲目执行后续步骤。
17. **配置注入**：DriverAgent 需与 ChatAgent、SubAgent 对齐，在启动时注入必要的运行时配置（LLM key、工具配置等），确保其能正常调用模型与工具。
18. **附件读取能力**：DriverAgent 支持读取当前会话中的附件（用户上传文件、步骤产出 artifact 等），用于判断生成物质量是否符合用户要求，作为裁决是否推进下一步的依据。

## 八、查询指令

19. **自然语言状态查询**：用户可通过对话询问「现在跑到哪一步了」「第2步的结果是什么」「有哪些步骤失败了」等问题；ChatAgent 通过查询类工具（`list_subagents`、`get_subagent_artifacts` 等）获取当前会话的插件执行状态后作答，不触发新的步骤执行。
