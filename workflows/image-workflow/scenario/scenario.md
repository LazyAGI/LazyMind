# AI 图片生成插件

## 场景描述

用于生成、查找、编辑图片，以及制作静态表情包、动态 GIF 和多状态表情包。
启动前的 Ask 只判断用户的主体、操作和交付形式是否足够执行：信息足够就直接开始，
只在缺少必要信息时询问缺失项，不在 Ask 卡片中输出方案、计划或创意分析。

## 工作流

1. **analyze_subject** — 识别行为模式、素材依赖和本任务精确需要的媒体能力；保存路由后由 Executor 强制运行代码级能力检测，不创建额外子 Agent
2. **collect_materials（可跳过）** — 仅在依赖上传图、知识库、显式搜索或外部参考时执行
3. **optimize_prompt** — 生成文生图提示词、精确编辑指令、视频运动提示词或结构化 Meme 计划
4. **generate_image** — 所有路由都先生成或登记一张/一组无字基础图；按需展示直接生成图、首帧、尾帧和全部参考图
5. **enhance_image（可跳过）** — 消费基础图，执行局部编辑、生成视频/GIF，或直接在图片/GIF 上排布精确字幕；多项结果按页展示左侧输入图、右侧视频/GIF

## 行为路由

| WORKFLOW | 典型请求 | 路径 |
|---|---|---|
| `CREATE_NEW` | 画一张赛博朋克城市 | analyze+check → optimize → generate → end |
| `KB_STYLE` | 按知识库风格画图 | analyze+check → collect → optimize → generate → end |
| `REFERENCE_GENERATE` | 先找参考图再创作新图 | analyze+check → collect → optimize → generate → end |
| `FIND_AND_EDIT` | 找一张照片再局部修改 | analyze+check → collect → optimize → generate(stage) → enhance(edit) |
| `EDIT_UPLOAD` | 修改用户上传图 | analyze+check → collect → optimize → generate(stage) → enhance(edit) |
| `CREATE_STATIC_MEME` | 生成/修改图后配精确字幕 | analyze+check → [collect] → optimize → generate(base) → enhance(caption) |
| `CREATE_ANIMATED` / `ANIMATE_UPLOAD` | 普通动图 GIF | analyze+check → [collect] → optimize → generate(frame) → enhance(video→GIF) |
| `CREATE_ANIMATED_MEME` | 单张动态表情包 | analyze+check → [collect] → optimize → generate(frame) → enhance(video→GIF→caption) |
| `CREATE_MEME_PACK` | 一套多状态表情包 | analyze+check → [collect] → optimize → generate(N bases) → enhance(N items) |

`[collect]` 表示仅在请求需要外部素材时执行。

## 按任务检查依赖

`workflow_routing` 在行为模式后声明 `REQUIRES`，能力门禁只检查该路径真正会调用的依赖：

- 文本生成新基础图：`image_generator`
- 已有图需要视觉修改：`image_editor`
- 已有图仅后加字幕：`none`
- 视频/GIF：`video_generator,ffmpeg`；若还需要从文本创建首帧或明确要求的尾帧，再加 `image_generator`

检测由 `analyze_subject` Attempt 的强制 post-step 代码执行，不经过模型，也不会生成独立子任务。
缺少依赖时，预检会返回 `MEDIA_CAPABILITY_DEPENDENCY_MISSING`、中文原因和 `settings_url`；
Chat 展示可跳转配置卡片，不发起付费媒体调用，也不自动重试配置错误。

动画生成按 Doubao Seedance 的任务类型传递素材：首帧/尾帧分别使用 `first_frame`、
`last_frame` role 且 `ratio=adaptive`；普通参考图使用 `reference_image` role。两种任务模式
不会混传。用户上传三张参考图时，三张都会在 generate 步骤展示并走参考图模式；不存在的
素材槽不会显示。

普通多 GIF 请求也会在 `prompt_used` 中固定 `COUNT`（1–10）。generate 与 enhance 使用
同一顺序生成和保存素材，因此 10 个 GIF 会形成 10 个 composite 页面。增强页不使用内层
素材 Tab：每页只显示实际存在的一张输入图和一个最终输出，左右各占一列；旧会话只有一张
参考图时会把它复用到每个输出页。视频与 GIF 同时存在时优先在右侧展示视频，空槽不占位。

## 字幕与基础图

所有精确显示文字都不交给图片/视频模型绘制。`optimize_prompt` 将文字放在
`meme_generation_plan.caption` 中，`generate_image` 只生成或登记无字 `generated_base_image`；
如需改图，`enhance_image` 先执行编辑，再用 `meme_add_caption` 在 `caption_box` 内换行、缩放并水平/垂直居中。
字样、颜色、描边与位置按用户要求执行。球衣印字、招牌、雕刻、纹身等场景内实体文字仍属于图片编辑。

## 失败交约

`generated_base_image` 是 `generate_image` 的必产物。即使模型已配置，只要调用报错或没有返回
有效图片路径，并且本次一张基础图也没产生，就必须标记步骤失败。不能保存占位图、不能空成功，
不能继续到工作流结尾。Chat 需要告诉用户具体是哪个模型/工具、什么错误导致未出图。
