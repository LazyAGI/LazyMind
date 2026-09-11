# 飞书浏览器 UI 编辑

此流程只在用户明确要求使用浏览器插件/页面 UI，或需要复用浏览器登录态时使用。用户要求写入、追加或修改即已授权对应页面操作，不要重复询问确认；必须保持用户给出的文本和操作语义，不擅自润色、补编号或改写。

## 已验证的空白正文写入流程

1. 调用 `browser_open` 打开完整飞书 Wiki/Docs URL，保存返回的 `session_id`、`revision` 和 `page_state`。
   如果大工具输出被保存为 `tool_spills/browser_open_<hash>.txt`，该文件名不是会话 ID；必须从文件 JSON 的 `result.session_id` 读取以 `bs_` 开头的真实会话 ID。
2. 如果 `page_state.editor_mode` 为 `editable`，或页面显示精确的“编辑”模式标签，则页面已经可编辑。不要点击“编辑”或“可编辑文档”；飞书的“编辑”是当前模式选择器，不是进入编辑态的按钮。
3. 在最新快照中查找正文占位文字，例如“按 `/` 插入内容”“按“/”插入内容，或让 AI 帮我写”。忽略同时出现的 `readonly textbox`：它是飞书辅助输入节点，不能代表正文只读，也不能作为 `browser_type` 的输入目标。
4. 用占位文字的最新 `ref` 和 `expected_revision` 调用 `browser_click`。该引用可能对应 DOM 文字节点；Browser Controller 会通过 CDP content quad、文字 Range 或可见父元素计算真实点击位置。
5. 点击成功后调用 `browser_type_focused` 输入用户原文。必须设置 `verify_text` 为正文中有辨识度、最好靠近末尾的短文本，以排除只写入一部分或没有落入编辑器的情况。不要再对 readonly textbox 调用 `browser_type`。
6. `browser_type_focused` 已在扩展内执行可见文本校验：带 `verify_text` 且无错误返回时，页面写入已经生效，这是默认完成条件。立即结束工具链并报告完成；不要为了重复证明同一结果再调用 grep/read、`browser_wait`、`browser_screenshot` 或 `browser_snapshot`。
7. 只有用户明确要求确认“已经保存到云端”或持久化状态时，才在输入成功后额外调用一次 `browser_wait` 等待保存提示。匹配成功后立即结束，不再截图或快照；等待超时时如实说明“文字已写入页面，但未确认云端保存”，不得循环等待。

已验证动作序列：

默认：`browser_open` → `browser_click`（正文占位文字 ref）→ `browser_type_focused`（带 `verify_text`）→ 结束。

用户明确要求确认云端保存时：在默认序列后只增加一次 `browser_wait`（保存提示）→ 结束。

## 非空正文与失败回退

- 对人员为行、日期为列的飞书表格，先从同一次最新快照中唯一定位人员文字和日期表头的 ref，再调用 `browser_click_intersection`，参数分别作为 `row_ref`、`column_ref` 并携带 `expected_revision`。确认返回的 `interaction.row.name`、`interaction.column.name` 与用户目标一致后，调用 `browser_type_focused` 写入原文并设置 `verify_text`；输入成功即结束。空目标单元格即使没有自己的 ref，也不需要 VLM。
- 行标签或列标题存在多个同名节点时，不得随意选择；先结合附近可见文本消歧。仍无法唯一定位时停止写入，避免写错人员或日期。
- 用户要求追加且正文非空时，先用最新快照唯一定位末尾正文块；点击该块后用 `browser_press` 的 `End`、必要时 `Enter` 建立追加位置，再用 `browser_type_focused`。无法唯一判断正文末尾时不要冒险覆盖，改用可唯一定位的飞书块 API，或说明需要用户给出插入位置。
- 页面结构变化后旧 ref 会失效；遇到 `STALE_SNAPSHOT` 时重新 `browser_snapshot`，只使用新 revision 的 ref。
- 遇到 `ELEMENT_NOT_VISIBLE` 时先重新 snapshot、滚动或尝试正文文字 ref。`browser_visual_inspect` 仅用于只读确认当前画面中的弹窗、错误或编辑状态，不能提供或替代点击坐标；所有操作仍必须使用 DOM ref 或 `browser_click_intersection`。
- `TYPE_NOT_APPLIED` 表示文字没有出现在页面，不得声称成功。重新检查是否误聚焦标题、评论框、搜索框或 readonly 辅助输入节点；最多换一种明确定位方式重试一次。
- `browser_type_focused` 未携带 `verify_text` 时，不能仅凭工具调用成功判断写入完成；此时只补一次有针对性的可见文本检查，不要组合截图、快照和多轮 grep 进行重复验收。
- 工具输出被保存到 `tool_spills` 时，成功状态仍由工具调用结果决定。若确实需要从 spill 中提取会话 ID、revision 或交点信息，把所需字段合并为一次检索并最多读取一次相关片段；不要对同一 spill 逐字段反复 grep/read。
