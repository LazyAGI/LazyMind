# AI 图片生成插件

## 场景描述

帮助用户生成、查找或编辑图片，以及生成 **动态表情包（GIF）**。工作流由 ChatAgent **动态路由**（dynamic 模式）。所有流程统一先分析，再进入素材收集，再优化 prompt，最后按工作流分支到生图或编辑。动态表情包仍走 **generate_image**（内部：并行 `video_generator` → 并行 `video_to_gif` → **串行 append** `save_artifact`），然后直接结束。

步骤：

1. **analyze_subject** — 仅做用户可读分析（`subject_analysis`）+ 内部路由（`workflow_routing`），**不调用工具**
2. **collect_materials** — 统一信息收集入口（可 `kb_search` + `web_search`），搜集参考图 / 编辑底图 / 动画首帧
3. **optimize_prompt** — 文生图 prompt、编辑指令，或视频运动 prompt
4. **generate_image** — 文生图，或动画：并行 N 次 `video_generator` → 并行 N 次 `video_to_gif` → 按 i 串行 append 保存（首次不传 sort_order）
5. **enhance_image** — 图生图编辑（image_editor）

结果 Tab 使用 **composite** 布局：多 GIF 靠各 list slot **按相同顺序依次 append** 对齐行；`sort_order` 仅用于覆盖已有项：`(原始图片 image_output, 动态 GIF gif_output)`。

## 动态路由

| WORKFLOW | 示例 | 路径 |
|---|---|---|
| `CREATE_NEW` | 「画一张赛博朋克城市」 | analyze → collect → optimize → generate → end |
| `KB_STYLE` | 已选知识库 + 「根据知识库中的风格画图」 | analyze → collect(kb/web) → optimize → generate → end |
| `REFERENCE_GENERATE` | 「先找几张赛博朋克参考图，再画一张类似的」 | analyze → collect → optimize → generate → end |
| `FIND_AND_EDIT` | 「找哈兰德照片，加红色王老吉」 | analyze → collect → optimize → enhance |
| `EDIT_UPLOAD` | 用户已上传 + 「加水印」 | analyze → collect → optimize → enhance |
| `CREATE_ANIMATED` | 「给我生成一个/三个动态表情包」或「找一张XX做成动态表情包」 | analyze → collect → optimize → generate → end |
| `ANIMATE_UPLOAD` | 用户已上传图 + 「做成动态表情包」 | analyze → collect(上传首帧) → optimize → generate → end |

### CREATE_ANIMATED 示例

```
用户: 给我生成三个猫咪眨眼的动态表情包

1. trigger_image_plugin / advance_step(analyze_subject)
   — WORKFLOW: CREATE_ANIMATED；NEXT_STEPS: collect_materials,optimize_prompt,generate_image
2. advance_step(collect_materials) — 可选参考图；描述已够清晰时可轻量收集
3. advance_step(optimize_prompt) — 英文视频运动 prompt（贴纸风、可循环短动作）
4. advance_step(generate_image)
   — 解析 N=3；同一轮发出 3 次 video_generator（prompt 带 "Sticker i of 3"；视频侧最多同时 3 路）
   — 下一轮发出 3 次 video_to_gif（同样并行）
   — 按 i=1..3 **串行** append 保存（**不传** sort_order）：image_output / gif_output（可选 video_output），caption='Sticker i'
5. advance_step("__end__")
```

同一张底图做多个：collect 存 1 张 origin；generate 在一次响应里对同一 urls 发出 N 次 video_generator。
多张不同底图：collect 存 N 张 material；generate 在一次响应里用不同 urls 发出 N 次 video_generator。
首次全量生成用依次 append 对齐行；局部重生成某一张时才传 sort_order 覆盖。

### CREATE_ANIMATED（联网找图再做 GIF）示例

```
用户: 联网找一张哈兰德的照片，做成动态表情包

1. analyze_subject — WORKFLOW: CREATE_ANIMATED（不要判成 FIND_AND_EDIT）
2. collect_materials — 搜图并保存 image_output（首帧, sort_order=1）+ material_images
3. optimize_prompt — 运动 prompt，保持主体可识别
4. generate_image
   — video_generator(urls=[首帧], duration=5) → video_to_gif
   — 串行 append：保留已有 origin；gif_output 不传 sort_order（caption='Sticker 1'）
5. __end__
```

### ANIMATE_UPLOAD 示例

```
用户: [已上传一张图] 把这张图改成动态表情包

1. trigger_image_plugin → analyze_subject — WORKFLOW: ANIMATE_UPLOAD
2. advance_step(collect_materials) — find_user_attachment → 保存 material_images + image_output（同一上传图，首帧）
3. advance_step(optimize_prompt) — 写运动描述，保持主体可识别
4. advance_step(generate_image)
   — video_generator(urls=[首帧]) → video_to_gif
   — 串行 append：image_output 保留原始图；gif_output 存 GIF（不传 sort_order；勿把 GIF 写入 image_output）
5. advance_step("__end__")
```

### KB_STYLE 示例

```
用户: [已选择知识库] 根据知识库中的风格，画一张产品宣传图

1. trigger_image_plugin / advance_step(analyze_subject)
   — KB 预取注入 objective；SubAgent 仅用文本命中写 subject_analysis，不对 KB 图调 VLM；可选最多 3 张 material_images
2. advance_step(collect_materials) — 用 kb_search / web_search 收集 1–3 张参考图
3. advance_step(optimize_prompt) — 融合 KB 风格写英文 prompt
4. advance_step(generate_image) — image_generator
5. advance_step("__end__") — 纯生成完成
```

前提：前端会话需传入 `filters.kb_id`（与 Chat 选知识库一致）。
若 analyze 之后才选择知识库，需 **重跑 analyze_subject**。

### FIND_AND_EDIT 示例

```
用户: 找一张哈兰德的照片，给他衣服上加上红色的王老吉三个字

1. trigger_image_plugin → analyze_subject
2. advance_step(collect_materials) — 搜图并保存 raw_source_image（底图）+ material_images
3. advance_step(optimize_prompt) — 写英文编辑指令 prompt_used
4. advance_step(enhance_image) — image_editor 编辑
```

全程不调用 image_generator。

### 路由规则

1. 读 `workflow_routing` 的 `WORKFLOW` / `NEXT_STEPS`（用 `get_step_result('analyze_subject')` 或会话 artifact 摘要；**不要**从 `subject_analysis` 正文解析路由字段）。
2. **analyze_subject**：需求分析给用户看（`subject_analysis`）；路由元数据写入 `workflow_routing`（不在分析 Tab 展示）；本步不调用任何工具。
3. ChatAgent 收到 analyze 通过后，根据 `workflow_routing` 的 NEXT_STEPS `advance_step` 到下一步。
4. 收到「Step X passed review」类系统消息后，**必须** 读取 `workflow_routing` 中的 NEXT_STEPS，并 `advance_step` 到下一步；不要停下来问用户要图。
5. `FIND_AND_EDIT`（如「找哈兰德照片改成 Q 版」）：即使用户会话里存在历史附件，只要本轮是「先找图再编辑」，就应判为 FIND_AND_EDIT，analyze 完成后 **必须** `advance_step(collect_materials)`，由 collect 步骤去搜图（**最多 1–3 张**）。
6. `advance_step` 的 step_id 必须在工具列出的 Available steps 中。
7. 所有 WORKFLOW：analyze_subject 完成后都先进入 `collect_materials`，由 collect 执行 kb/web 素材收集或上传图确认。
8. collect_materials 完成后只能 `advance_step(optimize_prompt)`（不允许直接去 generate 或 enhance）。
9. 编辑类请求（FIND_AND_EDIT / EDIT_UPLOAD）禁止 advance 到 `generate_image`。
10. optimize_prompt 完成后：生成类（含 CREATE_ANIMATED / ANIMATE_UPLOAD）→ `generate_image`；编辑类 → `enhance_image`。
11. EDIT_UPLOAD / ANIMATE_UPLOAD：collect_materials 负责确认上传原图，再 optimize → 对应终点步骤。
12. 动态表情包意图优先：用户说 动态表情包 / 动图 / GIF 时选 CREATE_ANIMATED 或 ANIMATE_UPLOAD，
    即使同时说「找一张」也**不要**判成 FIND_AND_EDIT；仍走 `generate_image`（视频→GIF）后结束。
    collect 若搜到底图，保存为 `image_output`；generate 步有底图则带 `urls` 做图生视频；
    GIF 只写入 `gif_output`，**禁止**用 GIF 覆盖 `image_output`。

## 有活跃会话时

| 用户意图 | 步骤 |
|---|---|
| 重新收集素材 / 换底图 / 换首帧 | collect_materials |
| 重查知识库 / 换 KB 风格参考 | analyze_subject |
| 重优化 prompt | optimize_prompt |
| 重新文生图 / 重新生成动态表情包 | generate_image |
| 重新编辑 | enhance_image |
