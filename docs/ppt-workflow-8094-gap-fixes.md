# PPT Workflow：8094 缺口与 8093 修复提取说明

## 1. 基线与目的

- 接入基线：`workflow_dev`，基线提交 `aa27f0f9`。其中 `fae93aa0` 已整体接入原 `cst/user_workflow_ui@d7a3ded0` 的 PPT 优化结果（含 `926f3f89`），本 PR 不再重复携带那 4 个历史提交。
- 参考实现：本地 8093 最终提交 `f87d9019` 及其历史提交。
- 本 PR 不是把 8093 整体 cherry-pick 到 8094，而是只提取 8094 仍缺失的 PPT 状态模型、并发隔离、编辑能力和 PPT 输入防护，并保留 8094 已经完成的 JSON 局部纠正、失败页复用、发布完整性检查、KB 参数适配、缺图降级、图片加载恢复和放大预览翻页。通用 Workflow Runtime、远程执行器、artifact 客户端和 Chat 路由保持 8094 原样。

## 2. 现有 8094 缺陷与本 PR 的解决方式

| 编号 | 8094 现有缺陷 | 用户影响 | 本 PR 的解决方式 | 主要来源 |
| --- | --- | --- | --- | --- |
| 1 | 风格选择与 AI 底图由旧的 `ppt_mode` 间接绑定，缺少两个正交状态 | 关闭 AI 底图时无法稳定进入三选一；直接生成也可能没有先锁定唯一样式；复用 deck 时旧底图状态可能覆盖本次选择 | 新增 `style_flow=auto/preview_choice`，与 `generate_background_images=true/false` 独立保存和路由；覆盖四种组合；生成大纲或底图前必须先得到唯一 `style_spec`；显式关闭底图会覆盖 deck 中旧值 | `74a68b81` 的风格流程，本 PR 按 8094 延后大纲结构的实现重新适配 |
| 2 | 阶段开始时全局 `set_llm_impl/set_vlm_impl`，结束时清空 | 两个 PPT 任务并发时可互相覆盖模型调用，或由先结束的任务提前清空另一个任务的 callable | `llm_call` / `vlm_call` 随当前任务显式传入每个阶段函数和批处理子任务，不再修改全局模型适配器 | `e54118ad` |
| 3 | `slide_intent`、`visual_hints` 与读者可见文案边界不完整，旧 `outline.md` 甚至把 `visual_hints` 列为可见字段 | “本页用于……”“画面采用……”“布局……”等制作说明可能出现在 PPT 正文 | 提示词明确内部字段；大纲对元话语做本地校验并局部纠正；确定性页面 query 删除 `slide_intent` 和遗留元话语；页面生成及重写提示词再次禁止泄漏 | `5f1fc070` |
| 4 | HTML 幻灯片只能选择一个 `[data-el]` 元素 | 无法一次修改同组卡片、步骤或编号列；多次逐项改写慢且结果可能不一致 | 支持 Shift 增减选择、显式 `data-group`、同行 flex 与合法编号纵列的隐式分组、联合选择框和数量提示；改写弹窗打开时仍可增减目标；完整 `targets/scope` 通过请求边界传给后端，由后端在一个预览/提交中原子应用并拒绝父子节点冲突 | `74a68b81`、`f87d9019` |
| 5 | 旧 HTML 没有 `data-el` 时无法精确选中 | 旧 PPT 页面可能完全点不中，只能重绘整页 | 前端为可视对象计算 `dom_path`、`tag` 和临时稳定 ID，并原样送入改写请求；后端校验路径、拒绝非视觉结构，并在提交前物化稳定锚点 | `74a68b81`、`f87d9019` |
| 6 | 外部/native PPT 的整页栅格背景缺少代理编辑语义 | 修改前景文本时代理仍透明；删除前景可能把原始整页背景一起删除 | 识别 `data-lazymind-ppt-proxy`；文字/样式修改只显现被改代理；删除时清空代理内容并保留覆盖色和原始栅格背景 | `f87d9019` |
| 7 | 通用小弹层没有区分 HTML 幻灯片、渲染图片和普通文本/文件；图片历史上下堆叠，HTML 历史预览可能再次出现“放大”；后端未返回 `revision_count` 时入口消失；版本标识位置也不符合 PPT 工作区习惯 | 16:9 页面预览过小、版本间不便比较；历史预览会产生嵌套弹层；用户可能看不到已有历史或误以为回滚会删除后续版本 | HTML 幻灯片与渲染图片统一改为“左侧版本列表＋右侧完整预览＋底部来源/时间/当前状态/应用版本”；HTML 历史预览禁用二次放大；普通文本/文件继续使用各自历史视图；只要存在已持久化 revision 即显示入口；版本标识固定在幻灯片画面内左下。回滚只重新选择指定 revision，不删除其他历史 | `74a68b81` |
| 8 | WorkflowPanel 只有“用户点过后不再自动跳”的隐式行为，且用数字下标保存当前 Tab | 用户不知道当前是跟随还是自由浏览；动态条件 Tab 插入/删除时，同一下标可能变成另一页；失败/审批步骤可能定位不准 | 新增按 session 保存的“跟随进度/自由浏览”；根据 Runtime projection 定位当前、失败、审批等待及最后执行步骤；以 Tab ID 而不是数组下标保存当前页，条件 Tab 出现/消失时保持原 Tab 身份；支持 YAML 默认展开和默认跟随；避免重复状态点；回退确认使用本地化步骤名称 | `74a68b81`、`5f1fc070` |
| 9 | 只给风格选择 Tab 配置了跳过隐藏；素材和底图步骤即使被跳过仍显示，完成后也无法可靠启用原先不存在的条件步骤 | 首次运行出现空 Tab；用户后续要求补素材或开启 AI 底图时，流程可能直接落到最终生成，隐藏步骤不会动态恢复 | 给素材、风格、底图提示词和底图生成声明各自的 `hide_when_material`；规划完成前先隐藏条件 Tab；Runtime 真正进入被跳过步骤时，以有效 attempt 或 projection 重新显示；完成后编辑若目标步骤首次被跳过，则先回退 `analyze_requirements` 重算适用性 | `74a68b81` |
| 10 | PPT 审批后的“继续/继续执行/continue”可能被作为后续步骤 `user_input` | 审批控制词污染或替换最初 PPT 要求，导致后续生成偏题 | 仅当当前 `workflow_id` 是 `ppt-workflow` 时，将纯继续指令识别为控制动作并写入空步骤输入；其他 Workflow 仍原样收到“继续”，真实 PPT 编辑要求也原样传递 | `5f1fc070` 的问题识别，本 PR 收窄为 PPT 专用保护 |

## 3. 风格流程状态模型

两个选择互不推导：

| 风格流程 | AI 底图 | 路径 | 最终约束 |
| --- | --- | --- | --- |
| `auto` | 关 | 直接生成唯一样式 → 大纲 | 大纲前必须存在唯一 `style_spec` |
| `auto` | 开 | 直接生成唯一样式 → 底图提示词/底图 → 大纲 | 底图和后续页面共用同一 `style_spec` |
| `preview_choice` | 关 | 三套预览 → 用户选 A/B/C → 大纲 | 关闭底图不影响三选一 |
| `preview_choice` | 开 | 三套预览 → 用户选 A/B/C → 底图提示词/底图 → 大纲 | 选择后锁定样式，所有后续步骤沿用 |

说明：三选一的产品概念不是本 PR 新发明的能力；本 PR 修复的是 8094 缺失的独立状态模型和四组合路由，避免把“三选一”和“是否生成 AI 底图”继续捆绑。

## 4. 为什么不提取早期串行锁

- `44b7474d`、`581af58b` 是任务级隔离完成前的中间加锁方案，只能把 PPT 模型调用串行化。
- `e54118ad` 已用任务级 callable 注入取代全局切换，允许不同 PPT 任务并行。
- 因此本 PR 只提取最终方案，不把三个提交计为三个独立功能，也不同时保留锁和任务级注入。

## 5. 与 8094 已有能力的边界

以下能力由 `926f3f89` 或最新 8094 已实现，本 PR 保留并适配，不重复声明为新增：

- JSON 局部纠正与最多三次总尝试。
- 大纲可编辑文字在前、内部结构转换和校验后移到最终生成阶段。
- 失败页面复用、发布完整性检查和缺图降级。
- KB 代理入口参数适配。
- 每页一张内容图片的提示词约束。
- 图片加载恢复和放大 PPT 左右翻页。

本 PR 也不包含以下已拆分事项：

- `151dc290` 的发布成功后立即终止修复。
- `9486a829` 的 KB `kb_ids` 入口归一化；8094 的 `926f3f89` 已有对应适配。

## 6. 主要改动位置

### PPT Workflow 与运行时

- `workflows/ppt-workflow/workflow.yaml`
- `workflows/ppt-workflow/scenario/state.yml`
- `workflows/ppt-workflow/scripts/tools.py`
- `workflows/ppt-workflow/runtime/scripts/run_stage.py`
- `workflows/ppt-workflow/runtime/prompts/outline.md`
- `workflows/ppt-workflow/runtime/prompts/page_html.md`
- `workflows/ppt-workflow/runtime/prompts/page_html_rewrite.md`

### PPT 输入边界

- `algorithm/lazymind/chat/workflow/workflow_manager.py`

### 前端

- `frontend/src/modules/chat/components/WorkflowPanel/ppt/SlotHtmlSlide.tsx`
- `frontend/src/modules/chat/components/WorkflowPanel/SlotComponents.tsx`
- `frontend/src/modules/chat/components/WorkflowPanel/index.tsx`
- `frontend/src/modules/chat/components/WorkflowPanel/workflowFollowMode.ts`
- `frontend/src/modules/chat/components/WorkflowPanel/panelExpansion.ts`

## 7. 验收重点

1. 四种风格/底图组合均能从启动走到大纲；关闭底图时仍可三选一；直接生成先得到唯一样式。
2. 两个并发 PPT 任务分别使用自己的 LLM/VLM callable，不读写全局模型实现。
3. 大纲和最终页面不出现制作元话语；旧输入中的元话语不会进入页面 query。
4. Shift 多选、显式/隐式分组、弹窗内继续增减目标、父子冲突拒绝均可回归；`targets/scope/dom_path/tag` 不在请求边界丢失。
5. HTML 幻灯片和渲染图片历史均使用左侧版本列表、右侧完整预览，显示来源/时间/当前状态并可应用旧版本；HTML 历史预览没有二次“放大”入口；缺少 `revision_count` 时仍可打开；回滚后其他历史保留；讲稿区使用紧凑外观，版本入口固定在左下。
6. WorkflowPanel 的跟随/自由模式按 session 保存，失败和审批步骤定位正确；条件 Tab 插入或删除后仍保持原 Tab ID。
7. 被跳过的素材/风格/底图 Tab 首次隐藏；完成后重新启用对应能力时先重算适用性，Runtime 进入步骤后 Tab 动态恢复；stale attempt 不会误恢复。
8. PPT 会话中的纯“继续”不会成为步骤业务输入，真实 PPT 修改指令仍会传入；非 PPT Workflow 的“继续”保持 8094 原行为。

## 8. 与最新 `workflow_dev` 的冲突复核

本次不是只清理 Git 标红，而是按最新 8094 重新核对了提交历史、双方同时修改的文件和自动合并后的行为：

- 原 PR 最早的 `14ff7a47`、`65abcf12`、`926f3f89`、`d7a3ded0` 共 54 个文件，已由 8094 的 `fae93aa0` 接入。已从本 PR 重放范围中删除，避免重复代码、重复文档和用旧实现覆盖 8094 后续演进。
- 旧分支直接合并会在 10 个文件产生文本冲突；本 PR 从 `aa27f0f9` 重新整理最终差异，不保留“先改通用链路、再回退”的历史噪声，最终没有冲突标记。
- 对 `d7a3ded0` 之后双方仍共同演进的文件逐项核对：`request.ts` 只扩展 PPT 多目标选择请求；中英文文案只新增 PPT Workflow 跟随/自由浏览键；通用远程执行、artifact、Chat 路由均不在本 PR 修改范围。
- 8094 已有的延后大纲结构化、JSON 局部纠错、失败页复用、发布完整性检查、KB 参数适配、素材缺图降级、图片加载恢复以及放大后左右翻页全部保留；PR 的 HTML 编辑、版本历史和风格状态模型在这些实现上扩展。
- 保留了 8094 大纲提示词中“少字／大图／留白／minimal／magazine style 优先”的密度规则，同时修正 `slide_intent`、`visual_hints` 被当作读者可见字段的语义冲突。
- 8094 原有 Runtime 回执结构和 `_compact_model_frontier` 行为保持不变；本 PR 不修改其他 Workflow 的模型工具协议。
- WorkflowPanel 的现有聚焦 Tab、默认放大和左右翻页接口没有被替换；显式跟随/自由模式、条件 Tab 动态恢复和 PPT 版本入口与其组合运行。

最终相对 `workflow_dev@aa27f0f9` 只保留 PPT Workflow、PPT 编辑/展示及 PPT 输入保护所需的 32 个文件改动；未带入最新基线的其他文件变化，也未修改 LazyLLM 子模块指针。

## 9. 回归结果

- 前端专项：6 个测试文件、97 项通过，覆盖 `SlotHtmlSlide` 默认放大与左右翻页、多目标编辑、版本历史、WorkflowPanel 跟随模式、动态 Tab 和 store 投影。
- 后端专项：141 项和 12 个子测试通过，覆盖 PPT 风格/页面/编辑、制作元数据防泄漏、任务级模型隔离，以及 PPT 专用“继续”输入保护和非 PPT 原行为保持。
- 前端生产构建通过；本次变更的前端生产文件 ESLint 通过。
- `git diff --check` 通过；PPT `workflow.yaml` / `state.yml` 均可解析为 8 个步骤；变更的 Python 文件语法检查通过。
- 全仓 `tsc --noEmit` 仍会命中 8094 基线中既有的测试类型和其他模块错误；本次修改的生产文件没有新增 TypeScript 诊断，因此不把“全仓类型检查通过”写入 PR 结论。
