# AI 图片生成插件

## 场景描述

帮助用户生成、查找或编辑图片。工作流由 ChatAgent **动态路由**（dynamic 模式）。所有流程统一先分析，再进入素材收集，再优化 prompt，最后按工作流分支到生图或编辑。

步骤：

1. **analyze_subject** — 仅做用户可读分析（`subject_analysis`）+ 内部路由（`workflow_routing`），**不调用工具**
2. **collect_materials** — 统一信息收集入口（可 `kb_search` + `web_search`），搜集 **1–3 张**参考图或编辑底图
3. **optimize_prompt** — 文生图 prompt，或编辑类 WORKFLOW 的编辑指令
4. **generate_image** — 文生图（CREATE_NEW / KB_STYLE）
5. **enhance_image** — 图生图编辑（image_editor）

## 动态路由

| WORKFLOW | 示例 | 路径 |
|---|---|---|
| `CREATE_NEW` | 「画一张赛博朋克城市」 | analyze → collect → optimize → generate → end |
| `KB_STYLE` | 已选知识库 + 「根据知识库中的风格画图」 | analyze → collect(kb/web) → optimize → generate → end |
| `REFERENCE_GENERATE` | 「先找几张赛博朋克参考图，再画一张类似的」 | analyze → collect → optimize → generate → end |
| `FIND_AND_EDIT` | 「找哈兰德照片，加红色王老吉」 | analyze → collect → optimize → enhance |
| `EDIT_UPLOAD` | 用户已上传 + 「加水印」 | analyze → collect → optimize → enhance |

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
2. advance_step(collect_materials) — 搜图并保存 image_output（底图）
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
10. optimize_prompt 完成后：生成类 WORKFLOW → `generate_image`；编辑类 WORKFLOW（FIND_AND_EDIT / EDIT_UPLOAD）→ `enhance_image`。
11. EDIT_UPLOAD：collect_materials 负责确认上传原图，再 optimize → enhance。

## 有活跃会话时

| 用户意图 | 步骤 |
|---|---|
| 重新收集素材 / 换底图 | collect_materials |
| 重查知识库 / 换 KB 风格参考 | analyze_subject |
| 重优化 prompt | optimize_prompt |
| 重新文生图 | generate_image |
| 重新编辑 | enhance_image |
