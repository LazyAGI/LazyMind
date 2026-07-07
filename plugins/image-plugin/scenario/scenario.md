# AI 图片生成插件

## 场景描述

帮助用户生成、查找或编辑图片。工作流由 ChatAgent **动态路由**（dynamic 模式）。纯生成和 KB 风格生成不需要素材收集；只有需要进入 image_editor 的编辑工作流才准备底图。

步骤：

1. **analyze_subject** — 分析需求 + 路由；若已选知识库，**KB 检索在触发本步之前自动完成**并注入 objective。本步**禁止**对 KB 图片调用 VLM，仅用文本命中写分析；最多直接保存 3 张 KB 参考图 URL（无需 validate_image_ref）
2. **collect_materials** — 仅用于 FIND_AND_EDIT / EDIT_UPLOAD，准备 raw image + 编辑指令
3. **optimize_prompt** — 文生图 prompt（CREATE_NEW / KB_STYLE）
4. **generate_image** — 文生图（CREATE_NEW / KB_STYLE）
5. **enhance_image** — 图生图编辑（image_editor）

## 动态路由

| WORKFLOW | 示例 | 路径 |
|---|---|---|
| `CREATE_NEW` | 「画一张赛博朋克城市」 | analyze → optimize → generate → end |
| `KB_STYLE` | 已选知识库 + 「根据知识库中的风格画图」 | analyze(KB 预取) → optimize → generate → end |
| `FIND_AND_EDIT` | 「找哈兰德照片，加红色王老吉」 | analyze → collect → enhance |
| `EDIT_UPLOAD` | 用户已上传 + 「加水印」 | analyze → collect → enhance |

### KB_STYLE 示例

```
用户: [已选择知识库] 根据知识库中的风格，画一张产品宣传图

1. trigger_image_plugin / advance_step(analyze_subject)
   — KB 预取注入 objective；SubAgent 仅用文本命中写 subject_analysis，不对 KB 图调 VLM；可选最多 3 张 material_images
2. advance_step(optimize_prompt) — 融合 KB 风格写英文 prompt
3. advance_step(generate_image) — image_generator
4. advance_step("__end__") — 纯生成完成
```

前提：前端会话需传入 `filters.kb_id`（与 Chat 选知识库一致）。
若 analyze 之后才选择知识库，需 **重跑 analyze_subject**。

### FIND_AND_EDIT 示例

```
用户: 找一张哈兰德的照片，给他衣服上加上红色的王老吉三个字

1. trigger_image_plugin → analyze_subject
2. advance_step(collect_materials) — 搜图，并在本步内保存 material_images +
   image_output + prompt_used（编辑指令）
3. advance_step(enhance_image) — image_editor 编辑
```

全程不调用 image_generator。

### 路由规则

1. 读 `subject_analysis` 的 `WORKFLOW` / `NEXT_STEPS`（用 `get_step_result('analyze_subject')` 或会话注入的 artifact 摘要）。
2. **analyze_subject**：需求分析、路由；KB 检索由系统在触发前完成。联网搜图只在编辑类 FIND_AND_EDIT 的 **collect_materials**。ChatAgent 收到 analyze 通过后应 `advance_step` 到 `NEXT_STEPS` 的下一步。
3. 收到「Step X passed review」类系统消息后，**必须** 读取 `subject_analysis` 中的 WORKFLOW / NEXT_STEPS，并 `advance_step` 到下一步；不要停下来问用户要图。
4. `FIND_AND_EDIT`（如「找哈兰德照片改成 Q 版」）：即使用户会话里存在历史附件，只要本轮是「先找图再编辑」，就应判为 FIND_AND_EDIT，analyze 完成后 **必须** `advance_step(collect_materials)`，由 collect 步骤去搜图。
5. `advance_step` 的 step_id 必须在工具列出的 Available steps 中。
6. 编辑类请求禁止 advance 到 `generate_image`。
7. KB_STYLE / CREATE_NEW：analyze_subject 完成后直接 advance 到 `optimize_prompt`，禁止进入 `collect_materials` 搜图。
8. FIND_AND_EDIT：collect_materials 完成后 **必须** `advance_step(enhance_image)`，**禁止** advance 到 `optimize_prompt` 或 `generate_image`。
9. EDIT_UPLOAD：analyze_subject 完成后 advance 到 `collect_materials`，由 collect_materials 保存上传原图和编辑指令，然后进入 `enhance_image`。

## 有活跃会话时

| 用户意图 | 步骤 |
|---|---|
| 重新收集素材 / 换底图 | collect_materials |
| 重查知识库 / 换 KB 风格参考 | analyze_subject |
| 重优化 prompt | optimize_prompt |
| 重新文生图 | generate_image |
| 重新编辑 | enhance_image |
