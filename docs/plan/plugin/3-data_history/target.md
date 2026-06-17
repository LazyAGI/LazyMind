打通的生图管道基础上，叠加持久化版本历史、图片引用到对话框、组件内继续修改能力，完善插件框架的核心数据层。
1. 如果一个slot有多个artifacts，新产生artifacts时如何知道seq（机制保证，还是大模型算）  看起来是机制保证，会自动追加到后面
2. 如果删掉了某个artifacts，seq需要保证不能复用
3. artifacts的顺序是否重要（ppt场景重要，素材收集不重要）
4. 联网搜索产生的附件，如何入库，并展示在前端；能否从用户的知识库中获取信息（需要list出用户有哪些知识库，然后判断这些知识库的名称/类型/标签）
5. 用户的输入附件（图，文档）怎么处理，怎么让SubAgent拿到; 
6. 如果前端用户做了标记，怎么把标记给到ChatAgent判断意图，和给到SubAgent处理
7. 图片写入的时候，需要描述
8. plugin支持i18n
9. 用户的意图怎么识别，尤其是涉及第N个xx的时候；特殊的，某个阶段产生了5个附件，用户把第二个删了（前端已经看不见了），此时用户再说第2个，实际上指的是第三个，这种情况下要怎么处理
10. 每一轮对话的时候，如何带上前面的对话信息，及之前阶段的附件信息
11. artifact的类型N选1，需要实时判断，如pptx的每一页可以是图，也可以是前端代码
12. 跨阶段联合渲染，比如pptx场景，有每一页的描述，图（html）和讲稿，要一起渲染和调整顺序

## 来自 M2 的补充目标

13. 版本历史：三张表（plugin_session_steps / plugin_session_artifacts / plugin_session_versions）的数据层，HEAD 指针可移动，支持回退与分叉（回退后新 patch 以旧 HEAD 为 parent）
14. AI patch 快照：SSE patch 完成时自动打快照（change_source='ai'），流式过程中不逐 token 快照
15. 人工编辑快照：PATCH /artifacts 接口，Go 层防抖 5s 合并为一个版本（change_source='human'）；发送对话前强制快照保证版本连续
16. 大文本 OSS 存储：内容 ≥ 64KB 时改存 OSS/S3，content 字段存 {"type":"ref","url":"..."}（M2 暂缓，需在计划内明确实现时机）
17. 版本管理 REST API：GET /artifacts/:id（HEAD 内容）、GET /versions（版本树）、POST /rollback（移动 HEAD）
18. VersionHistory 前端组件：树形/列表展示版本，点击预览，支持「回退到此版本」
19. DiffViewer 前端组件：文本行级 diff + 图片并排对比，集成到 VersionHistory 对比模式
20. 图片引用到对话框：ImageCard 新增「引用此图片」按钮，点击后将 plugin_context（含 cited_image_url）注入对话框，PluginShell 自动填充 plugin_context 随消息发送