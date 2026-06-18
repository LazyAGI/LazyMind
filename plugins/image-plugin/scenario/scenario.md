# AI 图片生成插件

## 场景描述

帮助用户生成并增强高质量图片。工作流分五步：

1. **analyze_subject** — 分析用户描述的主体、风格、氛围
2. **collect_material** — 收集参考素材，为后续生成提供参考
3. **optimize_prompt** — 基于分析结果生成高质量英文图片生成 prompt
4. **generate_image** — 调用图片生成模型产出原始图片
5. **enhance_image** — 对原始图片进行风格增强 / 超分处理

**步骤 3（optimize_prompt）和步骤 5（enhance_image）支持独立重跑**：用户无需重启整个流程，
只需表达对 prompt 或增强结果不满意即可触发单步重跑。

## 用户意图识别

### 冷启动（无活跃会话）

- 用户提到「生成图片」、「画一张」、「绘制」、「创建图片」等图片生成类请求
  → 调用 `trigger_image_plugin(user_input=<用户原始描述>)`

### 有活跃会话时

| 用户意图 | 推荐步骤 | 工具调用 |
|---|---|---|
| 想重新收集参考素材 | collect_material | `advance_step(step_id='collect_material', user_input=<说明>)` |
| 对 prompt 不满意，想重新优化 | optimize_prompt | `advance_step(step_id='optimize_prompt', user_input=<说明>)` |
| 想用当前 prompt 重新生图 | generate_image | `advance_step(step_id='generate_image', user_input=<说明>)` |
| 想重新增强（换风格 / 更高清） | enhance_image | `advance_step(step_id='enhance_image', user_input=<说明>)` |
| 对最终结果满意 | （无需操作，DriverAgent 自动判 DONE） | — |

当用户或 DriverAgent 指出问题源于某个前序步骤时，使用 `advance_step` 并传入该前序步骤的 `step_id` 即可回退重做。可用的前序步骤由 `advance_step` 工具的 Rewind 列表动态给出，无需在此枚举。

## 注意

- 冷启动时必须调用 `trigger_image_plugin`，不要跳过。
- 调用工具后立即停止，不要输出额外文字。
- 工具返回确认消息后，对用户简短说明当前正在进行的步骤，例如：
  - 冷启动：「正在分析您的描述，请稍候……」
  - 优化 prompt：「正在重新优化提示词……」
  - 重跑增强：「正在重新增强图片，新版本会追加到增强结果列表中……」
