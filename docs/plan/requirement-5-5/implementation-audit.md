# 需求 5.5：现有实现、功能差异与代码复用审计

- 日期：2026-10-20（Asia/Shanghai）。
- 需求来源：[LazyMind 0.5 PRD · 5.5 统一对话筛选与批量处理范围](https://sensetime.feishu.cn/wiki/Skvrwyzpliqmb7kCEv4c8rotnwe#O1u6dg4M9oGel7xyDKBcZssIn8e)，已读取正文及蓝框示意图。
- 审计目标分支：`YuZou-coding/LazyMind:codex/requirement-5-5`。
- 固定目标提交：`3486910b9f761ae84f3f98b823bcb2e14d2500d4`。
- 当前工作区分支：`codex/requirement-5-5`，基线提交与上述固定目标一致；用户已完成本地分支切换。
- 审计方法：读取远端固定提交；部分本地文件仅在 Git blob SHA 与远端完全一致后用作阅读副本。MainLayout 等存在差异的文件以远端版本为准。
- 第 1–7 节为开发前的静态审计；第 8 节为用户确认口径；开发实现与实际验证结果见第 10 节。初审的场景推演不等同于运行结果。
- 初次审计仅产出本文档；现已开始业务实现，所有改动位于需求分支工作区，未提交、未推送。
- 开发前曾因 `.git/FETCH_HEAD` 写入权限受限未能切换分支；用户完成切换后已核验分支和基线。下文初审中的代码现状仍对应固定提交，实施更新见第 10 节。

## 1. 开发判断

现有代码已经具备跨区域筛选和批量处理的主体结构。PRD 中“只作用于最近对话”的背景描述，不能直接当作当前基线的完整现状。

已经可以复用的能力包括：

1. 单一模式/来源筛选状态，同一个搜索输入向主列表和组列表传参。
2. 主列表、组列表、组成员查询共同使用后端来源判定。
3. 置顶、组内、最近区域共用一份批量选择 ID。
4. 关键词搜索临时展开折叠组，组成员单组全选主动翻页。
5. 现成的批量导出、删除、归档编排，以及部分失败保留、整理锁和父子会话规则。
6. 现有前后端测试 fixture 和请求竞态处理模式。

需要补齐的重点是：

- **明确未完成的 PRD 要求**：三个操作入口的位置；搜索语义与展示去重之间的一致性。
- **与范围一致性直接相关的代码风险**：搜索后隐藏的旧选择仍可能被提交；组内加载更多的旧响应可能混入新搜索结果。
- **已确认的产品口径**：全局“全选”覆盖未加载页；保留普通/任务模式划分；组名命中时包含组内符合来源条件的会话，置顶成员只在置顶区展示一次。详见第 8 节。

不建议以完成百分比衡量；应按下面的能力矩阵逐项验收。

## 2. PRD 与现有实现对照

| 能力 | 当前实现程度 | 证据 | 开发含义 |
|---|---|---|---|
| 搜索覆盖置顶、对话组、最近 | 已接线，但结果语义未统一 | F2:1024–1064；F1:575–577；F3:68–81 | 保留一套搜索输入与现有列表，修复查询规则差异 |
| 来源筛选覆盖 LazyMind 和外部 Agent | 已有主体能力 | F1:575–577、1215、1359；F4:111–119；B1:14–44 | 继续复用来源状态和后端来源判定 |
| 组内及折叠组可被搜索 | 已实现关键词搜索的加载与临时展开 | F3:68–93、158–162 | 保留搜索时临时展开、清空后恢复折叠的行为 |
| 仅来源筛选时折叠组的处理 | 组内查询已过滤，但仍保持折叠 | F3:68–81、161 | 不能认定“未覆盖折叠组”；是否自动露出结果属于交互口径 |
| 置顶且已分组的会话只显示一次 | 已有分区排除和 ID 去重；存在搜索遗漏边界 | F1:360–361、442–459、587；F3:158 | 保留去重规则，先统一命中集合，不能简单取消 pinned 排除 |
| 跨区域、跨来源批量选择 | 已实现主体结构 | F1:195–201、442–459、1355–1361；T1:402、1178 | 沿用父组件统一选择，不另建区域选择 store |
| 批量导出、归档、删除 | 业务链路已存在 | F1:777–892、1195–1211；F6:42、73–97；B4/B5 | 本需求扩展正确的选择范围，不新增同类操作服务 |
| 来源/模式变化后的选择清理 | 已实现 | F1:223–230；T1:1178 | 可作为关键词变化后的处理基础 |
| 关键词变化后的选择清理 | 当前没有同步清理 | F1:537–546、798、848、866 | 隐藏选择风险已存在，需确定并实现明确的选择生命周期 |
| 全局“全选”覆盖所有匹配分页 | 当前仅选已加载项 | F1:442–449、1328–1331 | 若确定为全量全选，需补齐分页收集，不能把现状当作已经完成 |
| 单组“全选”覆盖后续页 | 已实现 | F3:135–155；T2:146 | 可复用分页、取消及失败处理思路 |
| 三入口移到“已置顶”行右侧 | 未实现 | F1:1215–1299；F7:270–278 | 搬移原控件，保留整理入口的位置与行为 |
| 整理会话位置保持不变 | 当前位于最近标题旁，有测试保护 | F1:1223；T1:192–198 | 保留整理断言，修改批量入口仍在旧位置的过时断言 |

蓝框内的三个入口是“筛选”“查看归档”“批量”。查看归档是导航入口，不是点击后直接批量归档当前列表。

## 3. 需要处理或明确的差异

### D1. 工具栏仍属于“最近对话”，与 PRD 位置不符

**确定性：静态确认；PRD 明确要求。**

- 置顶和组区在 F1:1215 先渲染，三个按钮仍在其下方的 recent header 内（F1:1219–1299）。
- 整理会话入口位于 F1:1223，应继续留在原位。
- 样式 F7:270–278 把工具栏放到 recent header 网格第二行，也需要一起调整。

**边界**：现有置顶分区为空时被过滤掉（F1:437–440），标题随之不渲染（F1:1141–1151）。直接把按钮塞进现有置顶标题，会导致无置顶会话、置顶项全部被筛除时入口消失。

**最小调整**：在原 RecordList 内保留稳定的顶部工具栏位置，复用现有三个控件及批量状态下的“操作/取消”；整理会话继续留在原 recent header。无需复制两套按钮 JSX、导航逻辑或文案。

### D2. 同一会话因分组或置顶而出现不同搜索结果

**确定性：查询条件与展示规则静态确认；具体页面效果尚未运行复现。与 PRD“组织方式不影响搜索、筛选”直接冲突。**

当前规则：

| 查询 | 关键词匹配字段 |
|---|---|
| 主列表 ListConversations | display_name、source_display_name、source_document_id（B2:2315–2317） |
| 组列表 ListGroups | 组名、项目路径，或成员标题/摘要命中（B3:201–205） |
| 组成员 GetGroup | 标题、摘要（B3:261–264） |
| 前端组名命中 | 将成员关键词置空，显示该组符合来源条件的成员（F3:69–72） |

可由代码推出的三类场景：

1. 会话仅摘要命中：未置顶的组成员能被组查询找到；置顶后被组区排除，主列表又不搜索摘要，可能消失。
2. 会话仅来源显示名/文档 ID 命中：未分组时能显示；分组后主列表不再负责该成员的展示，组查询不搜索这些字段，可能消失。
3. 搜索组名“项目甲”，组内置顶会话标题“预算”不含关键词：前端组名命中后放宽成员查询，但组区又排除置顶成员；主列表没有组名命中上下文，可能找不到“预算”。

**最小调整**：

- 明确共同的会话级关键词字段与大小写规则，在主列表、组查找和组成员查询中复用同一个查询辅助逻辑。
- 保留现有 ListConversations、ListGroups、GetGroup 和返回契约。
- 单独确定“组名/项目路径命中是否展示全组”，不能只统一会话标题/摘要就认为组名边界也解决了。
- 先确定会话是否命中，再决定只展示在哪个区域。不要通过同时在组区和置顶区显示同一会话来绕过遗漏。

### D3. 搜索变化后，已隐藏的旧选择仍可能被批量操作提交

**确定性：状态流与提交参数静态确认；选择应清空还是跨查询保留，需要明确产品口径。**

- F1:223–230 仅在 conversationFilters 变化时清空选择、退出批量并清理组成员缓存。
- F1:537–546 搜索变化只更新 keyword 和查询，不清理 checkedList。
- 删除、归档、导出直接使用 checkedList（F1:798、848、866）。

场景：选择 A → 输入仅匹配 B 的关键词 → A 隐藏，但仍在批量提交 ID 中。全选勾选状态又只比较数组长度（F1:1320–1326），新结果数量碰巧等于旧选择数量时还可能显示为“全选”。

**建议口径**：规范化后的模式、来源、关键词任一变化，就使旧批量选择失效；若产品希望跨搜索保留，必须显式展示隐藏选择范围，而不能只保留一个无范围说明的计数。

**最小调整**：复用现有清选路径和 checkedList，以同一查询范围管理选择与请求生命周期。集合状态以 ID 包含关系计算，避免仅比较长度。无需第二套选择 context/store。

### D4. 组内“加载更多”未覆盖旧请求失效与结果去重

**确定性：可从代码推出竞态路径；未做运行时复现。**

- F3:106–112 的 more() 在请求返回后直接追加成员，没有查询版本检查。
- F2:1043 的组组件 key 包含模式和来源，不含 searchText；关键词变化时组件仍可存活。
- 场景：旧关键词的下一页未返回 → 改关键词且新查询已完成 → 旧下一页返回，追加到新结果。
- append 路径没有按 ID 合并，也没有加载中互斥；重复请求同一页时存在重复成员风险。

**最小调整**：沿用现有 refreshVersion、selectionVersion、historyRequestRef 的失效机制，补齐 more() 路径的查询范围校验、请求中状态及 ID 去重。避免另建通用请求框架。

### D5. 全局“全选”与单组“全选”的分页语义不同

**确定性：现状静态确认；是否属于本轮必改取决于产品对“全选”的定义。**

- 全局全选：historyList 已加载根会话 + batchGroupMembers 已加载组成员，按 ID 去重（F1:442–449、1328–1331）。
- 单组全选：持续读取 nextPageToken，成功后才一次性改变选择（F3:135–155）。
- T1:402 的分页批量测试先手动加载更多，再点击全局全选；它不能证明未加载页被自动选中。

**建议口径**：“全选”选中当前模式、来源和关键词下所有可选择会话，不受分页加载进度影响；如保留只选已加载项，应明确对应文案。

**复用方式**：使用现有分页 API 和单组全选的成功/失败/取消语义，必要时仅抽一个实际共享的分页收集函数。当前没有证据必须新增全选专用接口。

**计数注意**：

- GetGroup 的 total_size 是关键词过滤前的来源可见数，而 next_page_token 按关键词命中数推进（B3:245–248、262–286）。
- TotalMemberCount 用于整体组/项目语义，可能包含当前筛选外成员。
- 应按最终去重且可选择的根会话 ID 计算本次选择数，不直接把以上计数相加。
- 删除/归档的父子联动可能影响侧聊，因此实际处理数量与前端选择的根会话数量不必相等。

## 4. 后续可直接复用的代码清单

| 资产 | 现有职责与复用方式 | 避免的重复实现 |
|---|---|---|
| F5:55–60、88–153：ChatConversationFilters、read/select/reveal 函数 | 继续统一管理 normal/task 与 sources，保留事件及已发现来源缓存 | 第二套筛选 store、重复 sessionStorage key、各区域独立来源判断 |
| F2:1024–1064：sidebarSearchText 与 groupSection 接线 | 保留一个搜索输入，把共同条件传到两条查询链 | 给各区域新增独立搜索框/状态 |
| F1:548–599：getHistory | 保留来源/模式/关键词分页、请求版本保护和 ID 去重 | 新建平行主列表服务 |
| F3:20–24：GroupBatchSelection | 父组件持有统一 checkedIds，组只上报成员和 toggle 操作 | 每个分组独立维护并同步已选集合 |
| F1:442–459：batchSelectableConversationIds、toggleBatchConversations | 复用 ID 集合及去重选择；根据最终全选口径扩展数据来源 | 把同一会话按来源或区域反复计数 |
| F3:68–155：load/refresh/more/selectGroup | 在原链路补查询一致性、失效和分页收集 | 新建全局列表组件或另一套分页状态 |
| F1:360–440 与 F3:158：conversationTree、置顶/分组展示规则 | 保留组织结构及同一 ID 只展示一次，修正命中集合 | 简单平铺列表导致父子结构、置顶、分组行为丢失 |
| F1:777–892：batchActionMenuItems、batchDeleteHistory、exportHistoryFn | 继续提交明确的选择 ID，复用弹窗和失败处理 | 按区域/来源分别新增导出、删除实现 |
| F6:42、73–97：ArchiveConversationModal | 复用 ID 去重、顺序归档、成功/失败 ID 回传 | 新增第二套批量归档服务 |
| F1:601–622：removeHistoryItems 及组变化事件 | 复用活动会话清理、侧聊联动与多区域刷新 | 各区域单独删除本地数据造成残留 |
| F1:1244–1295 与 F7：原 toolbar 控件/样式/i18n | 搬移控件并保留归档路由与批量菜单语义 | 重复按钮、重复翻译键和导航逻辑 |
| B1:14–44：ConversationSourceIDs | 主列表、组搜索、成员查询继续共用来源归属判定 | 仅凭 chat_executor 判来源；与外部 binding 可见性产生偏差 |
| B2:2281–2467 与 B3:175–286：现有列表/组接口 | 复用模式、来源、关键词、分页与组元数据 | 新建“全局会话”表、索引镜像或平行接口 |
| B2:2198–2271：批量删除及 ID 规范化 | 复用所属用户检查、输入去重、整理锁、事务与实际删除 ID | 绕开既有回收站和父子生命周期 |
| B4:232–296：ArchiveConversation | 复用归档目录归属、侧聊限制、父子展开和整理锁 | 为 5.5 放宽单独侧聊操作或跨用户归档 |
| B5:169–234：显式 ID 导出 | 沿用按用户和 ID 导出存储会话/历史 | 用“省略 ID”伪装全选，意外包含筛选外或归档会话 |

允许的小规模抽取应有两个以上实际调用点，例如统一会话关键词谓词、复用分页收集逻辑。不以本需求为由大范围拆分 RecordList 或引入新的状态管理框架。

## 5. 已有测试资产及证明边界

| 测试文件/定位 | 已证明的源码意图 | 后续处理 |
|---|---|---|
| T1:192，keeps the organizer action beside the recent-conversation heading | 整理入口在 recent header；198 行同时要求批量入口仍在旧 header | **修改过时的批量位置断言**，保留整理位置断言 |
| T1:200，opens archives for the current %s filter | 查看归档根据 normal/task 跳转 | 直接保留，验证搬移没有改变语义 |
| T1:402，keeps custom groups visible and includes paginated group members in batch actions | 跨组/最近选择，组内置顶只出现一次，加载后分页可选 | 复用 group/member/pinned/nextMember fixture；不视作全量全选覆盖 |
| T1:755，keeps the source and task scope for search and subsequent pages | 搜索和后续页携带来源/模式 | 扩展关键词切换与晚到响应场景 |
| T1:1178，scopes group pagination and batch selection to the current source and clears old selections | 来源切换后旧选择清空 | 复用为关键词变化的验收参考 |
| T2:93，temporarily expands search results without saving | 搜索临时展开，清空后恢复保存的折叠状态 | 当前以组名命中，补组名不匹配、仅成员命中的场景 |
| T2:146、168、185 | 单组全选主动分页；失败保持原选择；重置后旧分页不落地 | 复用 deferred promise 和分页 fixture |
| T1:319、338、391、1062；T2:205 | 部分失败保留、独立 fork、子会话选择限制、批量中禁止拖拽 | 保留已有生命周期与交互契约 |
| T3:17–181，TestGroupSourceFilter | 来源多选/规范化、模式、逐页去重、隐藏导入排除、关键词与来源交集 | 用同一数据集补主列表与组接口结果一致性 |
| T4:397、486 | 外部会话归属不能仅看执行引擎；托管会话与 native session 边界 | 复用现有 binding fixture |
| T5:87，TestConversationOrganizerProtectsSingleBatchAndFamilyDelete | 整理锁保护批量删除、归档和关联侧聊 | 保持原有保护，无需新建权限/锁测试框架 |

需要明确的覆盖不足：

- MainLayout.test.tsx:94–105 完全 mock RecordList；不能证明真实 MainLayout → RecordList → ConversationGroups → SidebarGroups 接线。
- T1 的跨区域测试自己构造 groupSection；后续应增加一条能覆盖真实组合接线的测试，复用现有 mock 基础。
- T1:109–113 的 ConversationsApiFactory 返回空对象；当前测试不能证明导出最终使用正确的 ID 集合。应扩充现有 mocks，而不是复制一套 API 测试环境。
- 阅读到测试代码只证明已有测试资产及断言范围，本次没有宣称它们实际执行通过。

## 6. 最小补充验收矩阵

| 类别 | 场景 | 预期/适用条件 | 优先复用 |
|---|---|---|---|
| 原文要求 | 三入口上移；保留整理位置 | 有置顶、无置顶、无结果、批量模式均有可用入口，只有一套控件 | T1:192 + 原 toolbar |
| 原文要求 | 置顶、折叠组、项目组、最近同时存在，混合多来源 | 所有区域执行同一来源条件，同一 ID 展示和计数一次 | T1:402、1178 + T3 |
| 原文要求 | 组名不命中，仅组内成员命中；成员原来位于后续页 | 可查询到该会话，搜索不持久改变折叠状态 | T2:93、146 |
| 原文要求 | 同一会话未分组/分组/置顶前后搜索 | 标题、摘要、来源字段按最终统一规则命中一致 | T3 + T4 的同源数据集 |
| 查询一致性 | 旧关键词加载更多晚于新关键词结果返回 | 旧页不追加到新结果；同一页重复点击不产生重复项 | T2 deferred promise |
| 选择口径 | 选择 A 后搜索 B | 若采用清选口径，A 不再出现在操作 ID；不能只检查已选数 | T1:1178 选择 fixture |
| 条件性需求 | 不滚动/不点更多，跨多个组和最近分页全选 | 仅在确定全量全选后要求：全部匹配根会话被去重选中；错误、取消、重试一致 | T1:402 + T2:146/168/185 |
| 业务复用 | 同一选择集合分别导出、归档、删除 | 请求根 ID 集合一致且无重复；保留父子联动、整理锁与部分失败语义 | 现有 mocks、T1 操作测试、T5 |
| 产品口径 | 搜索组名时，组内存在标题不匹配的置顶会话 | 按确认后的组名语义展示一次，不因分区消失 | T1 的 pinned/member fixture |
| 回归 | 来源/模式切换、断开 Agent、归档后刷新、子/侧聊 | 沿用既有可见性和生命周期限制 | F5、T1、T4、T5 |

## 7. 建议的最小修改范围

| 文件/区域 | 可能需要的修改 | 是否已有可复用主体 |
|---|---|---|
| RecordList/index.tsx | 原 toolbar 上移；查询范围变化时处理选择；全选集合语义（若确定全量）；稳定顶部容器 | 是 |
| RecordList/index.scss | 顶部标题/toolbar 布局，去除 recent header 内旧定位约束 | 是 |
| SidebarGroups.tsx | more() 的版本保护/去重/请求中状态；搜索与置顶归属协调；复用分页全选 | 是 |
| MainLayout.tsx / ConversationGroups.tsx | 仅在统一搜索上下文需要时调整传参；现有来源和模式链路已通 | 是，不应默认重写 |
| backend/core/chat/conversation.go | 统一会话级关键词条件 | 是 |
| backend/core/conversationgroup/handlers.go | 组命中子查询及成员查询使用同一会话匹配规则 | 是 |
| 小型共享关键词查询辅助函数 | 在确定共同搜索字段后复用同一谓词 | 可能合理新增，但应避免重复三份 SQL |
| 上述现有测试文件 | 修改旧位置断言，扩充真实接线、查询一致性、选择和竞态用例 | 是 |

在保留现有接口契约的方案下，没有必要修改生成的 API client、增加数据库表/迁移或新增导出/删除/归档接口。若后续全量全选出现经验证的规模问题，再单独评估 ID 查询或服务端选择能力，不在当前阶段预建。

建议实施顺序：

1. 按第 8 节已确认的产品口径实施，先确保正式实现位于新分支固定基线。
2. 原位调整 toolbar 和稳定顶部容器，保留整理及归档导航契约。
3. 统一会话搜索条件和“组名命中”语义，保证分区变更不改变命中集合。
4. 补齐选择生命周期及组分页请求失效，复用现有分页实现全量全选。
5. 修改和扩充现有测试，执行针对性前后端测试，再用混合区域/来源的真实页面验收。

## 8. 已确认的产品方案与实施默认行为

用户于 2026-10-20 15:07（Asia/Shanghai）明确回复“按推荐方案执行”。以下口径已确认，不需要再次询问。前面初审中关于这些口径的条件性建议，以本节决定为准。

| 问题 | 当前行为 | 已确认方案 |
|---|---|---|
| “全局”是否跨 normal/task | 前端保留单一模式，后端可省略模式参数查询两者 | 保留模式划分，在当前模式内覆盖全部来源和区域 |
| 全局“全选”范围 | 仅已加载项；单组全选会翻页 | 选中当前模式、来源、关键词下全部匹配的可选择根会话，包括未加载分页 |
| 统一搜索字段 | 主列表与组成员不同 | 统一标题、摘要、已有来源名称及文档 ID；不扩展聊天正文全文搜索 |
| 组名命中 | 组名命中可放宽成员查询；主列表没有同样上下文 | 展示该组符合当前模式和来源条件的会话，置顶成员在置顶区展示一次；组名不命中时按会话自身字段匹配 |
| 查询范围变化后的已选项 | 关键词变化仍保留旧选择 | 关键词、来源或模式变化时清空旧选择 |
| 无置顶会话时的入口 | 现有置顶标题会隐藏 | 顶部三个操作入口仍可使用；整理会话留在原位 |
| 批量操作与父子会话 | 已有权限、整理锁、归档/删除/导出及侧聊联动规则 | 复用现有接口与生命周期规则，独立 fork 与依附侧聊维持原有区别 |

以下两项作为实施默认细节沿用原行为，不额外扩大范围：仅改变来源时保留用户折叠状态（组内数据仍按来源过滤）；单组选择操作作用于该组展示的非置顶成员，顶部全局全选统一包含匹配的置顶成员。关键词搜索继续临时展开命中组。项目路径既有搜索能力应在统一规则时保留。

“全部会话”仍应遵守所属用户、外部导入可见性、归档/回收站隔离和临时会话规则。移动“查看归档”入口不代表把归档会话并入当前结果。

## 9. 固定版本证据索引

以下链接全部固定到审计提交；表格中的行号以该提交为准，后续代码变更后应重新定位。

| 标识 | 文件 |
|---|---|
| F1 | [frontend/src/modules/chat/components/RecordList/index.tsx][F1] |
| F2 | [frontend/src/layouts/MainLayout.tsx][F2] |
| F3 | [frontend/src/modules/chat/conversationOrganizer/SidebarGroups.tsx][F3] |
| F4 | [frontend/src/modules/chat/conversationOrganizer/ConversationGroups.tsx][F4] |
| F5 | [frontend/src/modules/chat/constants/chat.ts][F5] |
| F6 | [frontend/src/modules/chat/components/ArchiveConversationModal.tsx][F6] |
| F7 | [frontend/src/modules/chat/components/RecordList/index.scss][F7] |
| B1 | [backend/core/common/conversation_source.go][B1] |
| B2 | [backend/core/chat/conversation.go][B2] |
| B3 | [backend/core/conversationgroup/handlers.go][B3] |
| B4 | [backend/core/chat/recovery.go][B4] |
| B5 | [backend/core/chat/conversation_export.go][B5] |
| T1 | [frontend/src/modules/chat/components/RecordList/index.test.tsx][T1] |
| T2 | [frontend/src/modules/chat/conversationOrganizer/SidebarGroups.test.tsx][T2] |
| T3 | [backend/core/conversationgroup/source_filter_test.go][T3] |
| T4 | [backend/core/chat/conversation_logic_test.go][T4] |
| T5 | [backend/core/chat/conversation_group_test.go][T5] |
| T6 | [frontend/src/layouts/MainLayout.test.tsx][T6] |

[F1]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/modules/chat/components/RecordList/index.tsx
[F2]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/layouts/MainLayout.tsx
[F3]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/modules/chat/conversationOrganizer/SidebarGroups.tsx
[F4]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/modules/chat/conversationOrganizer/ConversationGroups.tsx
[F5]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/modules/chat/constants/chat.ts
[F6]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/modules/chat/components/ArchiveConversationModal.tsx
[F7]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/modules/chat/components/RecordList/index.scss
[B1]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/backend/core/common/conversation_source.go
[B2]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/backend/core/chat/conversation.go
[B3]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/backend/core/conversationgroup/handlers.go
[B4]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/backend/core/chat/recovery.go
[B5]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/backend/core/chat/conversation_export.go
[T1]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/modules/chat/components/RecordList/index.test.tsx
[T2]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/modules/chat/conversationOrganizer/SidebarGroups.test.tsx
[T3]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/backend/core/conversationgroup/source_filter_test.go
[T4]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/backend/core/chat/conversation_logic_test.go
[T5]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/backend/core/chat/conversation_group_test.go
[T6]: https://github.com/YuZou-coding/LazyMind/blob/3486910b9f761ae84f3f98b823bcb2e14d2500d4/frontend/src/layouts/MainLayout.test.tsx


## 10. 实施与验证记录（2026-10-20）

### 已落地

- 复用同一组工具栏 JSX：筛选、查看归档、批量固定在已置顶标题右侧；没有置顶结果时保留入口，整理会话仍在最近标题旁。批量状态下各区域顺序保持一致。
- 抽取 `common.ConversationKeywordIDs` / `ConversationGroupKeywordIDs`，由主列表、组列表、组成员查询共用。标题、摘要、来源显示名、来源文档 ID，以及组名/项目路径的命中规则保持一致。参数和字段统一由数据库 LOWER 处理，保留 ASCII 大小写不敏感及非 ASCII 大写精确命中；未扩展完整 Unicode 大小写折叠。
- 全局全选复用主列表分页接口，遍历当前模式/来源/关键词的全部页，按 ID 去重并排除依附侧聊；独立 fork 保留原有可选规则。完整收集成功后才替换选择，失败保留原选择；重复游标中止，不提交不完整集合。
- 批量导出、归档、删除继续提交同一份明确的会话 ID；保留既有权限、整理锁、部分失败处理及父子会话生命周期。跨页收集的整理锁信息也参与按钮可用性判定。
- 关键词、来源、模式变化清理旧选择、缓存和批量归档目标。全选请求在范围变化、取消、卸载或手动更改选择后失效；旧列表响应不能覆盖新查询。
- 组内加载更多和单组全选增加请求互斥、去重、失效与重复游标保护；新查询清除旧成员，避免查询失败时仍可选择旧结果。单组全选仍排除组内置顶成员，由顶部全选统一覆盖。

### 复用与新增边界

保留现有模式/来源状态、三类列表 API、会话排序与折叠规则、归档/删除/导出接口及现有测试 fixture。没有新增 API、数据库表或全局选择 store；主列表普通加载和全量全选复用同一个分页请求函数。新增的全量结果缓存仅记录完整选择集合及锁信息，不复制列表渲染或业务操作流程。

### 已执行验证

- 后端：`go test ./common ./conversationgroup ./chat -run 'TestConversationKeyword|TestConversationGroupKeyword|TestGroupSourceFilter|TestRenameConversationPersistsAcrossViewsWithoutChangingActivity|TestConversationPreviewSummaryIsScopedAndDoesNotChangeActivity|TestConversationOrganizerProtectsSingleBatchAndFamilyDelete' -count=1`，通过；`common` 编译通过，后两个包测试通过。
- 前端 RecordList：58 个测试通过，覆盖跨页集合、三种批量操作的一致 ID、去重/侧聊、失败重试、关键词清选、关键词/来源/模式/取消后的旧响应失效，以及有无置顶时工具栏和整理入口位置。
- SidebarGroups：46 个测试通过，覆盖组名关键词直传、查询变化清理旧成员、分页互斥/去重/重复游标、取消/卸载/晚到响应、失败重试及搜索下加载更多。
- MainLayout 与 conversationHistory 回归：40 个测试通过。MainLayout 使用现有组件 mock，因此不把该结果表述为完整浏览器端到端验证。
- Vite 生产构建通过，耗时约 27 秒。存在项目现有的大 chunk、Sass import 和依赖浏览器兼容性警告。
- 使用已安装的 Node 22.23.2 运行前端验证；默认 Node 20 与当前 jsdom/undici 不兼容，未修改依赖或项目环境配置。
- 完整 TypeScript 检查未通过，但将本次 4 个 TS/TSX 文件以 compiler host 读取 HEAD 内容后，按同一配置/依赖做基线对照，未发现新增诊断：RecordList 实现 8→7、测试 5→5，SidebarGroups 实现 2→2、测试 6→6。既有问题包括类型不兼容、旧测试必填 props 缺失、隐式 any 和依赖 JSX 类型；本需求未扩大修复范围。
- `git diff --check` 通过。前端合计 144 个相关测试通过；后端非 ASCII 回归先复现失败、再验证修复通过。

以上为首轮自动验证记录；后续最新运行环境的真实浏览器验收见下文。

### 本地联调前置检查（2026-10-20）

用户启动 `make local-up` 后检查到 runtime 保存状态为 ready，8090/5024/18001 等端口有监听；通过 Chrome 成功打开 `http://127.0.0.1:8090/login`，页面正常显示登录表单。

但当前运行的 core（PID 38292）仍加载 2026-10-19 15:26:42 +0800 的二进制，早于本需求改动；前端 dist 构建于 2026-10-20 15:22:12，包含主要新界面但早于最后源码改动。runtime-manager 的 up 在服务已健康时直接复用现有实例，所以重复 local-up 不能证明新代码已经运行。

完整真实页面验收尚未完成：需要先在用户终端执行 `make local-down`、`make local-up` 重新构建并启动（不执行 reset/数据清理），并在测试 Chrome 页面完成正常登录。此时不把单元测试结果或旧服务可访问视为最新代码的端到端验收通过。

### 登录后浏览器冒烟测试（2026-10-20）

已在 Chrome 登录本地 admin 账号并完成以下实际 UI 检查：

| 场景 | 浏览器结果 |
|---|---|
| 普通对话空列表、无置顶 | 筛选/归档/批量位于已置顶行右侧，整理结果入口仍在最近行；截图检查布局正常 |
| 取消唯一来源 | 显示“请至少选择一个”，LazyMind 仍保持选中 |
| 空列表进入批量并全选 | 无选中数量；导出/批量归档/删除菜单均禁用 |
| 任务模式两条现有任务全选 | 两行选中，全选计数为 2；取消一行后计数为 1 且全选呈半选 |
| 已有选择时输入不匹配关键词 | 退出批量，旧结果隐藏；再次进入批量时全选未选中且无残留计数 |
| 普通/任务模式查看归档 | 分别进入 kind=dialog 与 kind=task，归档页类型标签与当前模式一致 |

检查后清空搜索、退出批量并恢复普通对话首页；未对现有会话提交导出、归档或删除操作。

运行环境仍未重启：core PID 38292、Caddy PID 11266 和产物时间均保持不变。因此上述结果仅为当前页面的冒烟测试，组名/摘要统一搜索、外部来源交集、超过一页的全选和批量写操作仍未完成最新版本的真实联调。需要用户在有 Docker 权限的本机终端执行 local-down/local-up 后继续。


### 最新运行环境端到端验收与修复（2026-10-20）

用户重启后，已确认 core 产物更新于 15:46:24、前端 index.html 更新于 15:46:43；core PID 87490、Caddy PID 87562，使用重新构建的需求分支代码。后续组复选框修复又通过 Vite 构建（28.31 秒），刷新 Chrome 后复验。

通过正常 UI 创建 `QA55_联调_20261020` 测试组，一次最小回复建立种子会话，再通过既有独立分支功能建立 53 个副本，共 54 条普通会话。分布为：组内置顶种子 1 条、组内非置顶 52 条、最近区域自由会话 1 条。折叠状态通过产品 UI 保存。

| 场景 | 实际浏览器/文件证据 |
|---|---|
| 折叠组与跨区域全选 | 仅显示置顶和最近两行时，顶部全选计数为 54，包含未展开组和未加载页 |
| 组名精确命中 | 搜索 `QA55_联调_20261020` 后组临时展开，组内置顶种子仅出现一次；自由会话被排除 |
| 搜索结果超过首屏 | 首屏只有 50 个会话复选框且有“显示更多”，顶部全选计数为 53 |
| 搜索变更清选 | 已全选 54 条时输入组名，退出批量并清空原选择 |
| 批量导出 | 下载 `conversations_20261020_075922.csv`；读取文件确认 53 行、53 个唯一 conversation_id，全部标题以 QA55 开头，包含置顶种子且不含自由会话 |
| 全局/单组选择联动 | 修复后全选 54 时组为全选；取消组只剩置顶+最近 2 条；重新选组恢复 54；取消最近后计数 53，组仍为全选 |
| 批量归档 | 对 53 条选择归档到默认未分类；归档确认显示 53，主列表仅剩未选中的自由会话，归档出现三页测试会话 |
| 取消归档 | 通过现有 UI 恢复 53 条；归档分类为空，返回主列表以 QA55 搜索后全选重新为 54 |
| 批量删除 | QA55 搜索下全选 54；确认框明确为 54 条移入回收站、保留 30 天。执行后主列表不再有测试会话；回收站 QA55 搜索三页累计 54 个唯一标题，含置顶种子，末页下一页禁用 |

真实联调发现并修复一个此前自动测试未覆盖的边界：全局全选已选中未加载页，但组复选框仍只根据首屏和剩余游标判断为半选；取消组也只移除已加载项。现在复用主列表已完整读取的结果，通过可选 `matchingGroupMemberIds` 将每组非置顶 IDs 传入 SidebarGroups，用同一完整集合计算 checked/mixed 和切换整组；不重复发起分页查询。尚无完整集合时，选择和取消组都遍历余页并保留失败/过期保护。

补充验证：RecordList 62/62、SidebarGroups 50/50，连同此前 MainLayout/conversationHistory 40/40，相关前端测试共 152 个通过。新增回归覆盖完整组 map 排除置顶、包含未加载成员、取消组保留其他区域，以及关键词/来源/模式变化使旧 map 失效。最新生产构建和 `git diff --check` 通过。

本轮真实写操作仅作用于 QA55 测试会话；现有用户任务未修改。外部来源组合、摘要/文档 ID 字段命中、依附侧聊和整理锁仍以现有定向自动测试为证据，未伪称本次浏览器已覆盖这些数据类型。全量 TypeScript 检查仍有前述既有诊断，未扩大范围修复。

测试结束状态：已退出批量、清空搜索并返回普通对话首页。54 条测试会话保留在可恢复回收站，未清空回收站。空测试组 `QA55_联调_20261020` 与本地导出 CSV 保留；未对没有恢复入口的组执行永久删除。代码仍在 `codex/requirement-5-5` 工作区，未提交/推送。

## 11. 独立审查后的修复与验证（2026-10-20）

本节记录独立审查确认的四项问题及修复，更新第 10 节中关于全量分页、完整组 ID 缓存复用和独立 fork 的实现结论。前面的联调记录保留为当时版本的历史证据，不代表本次修复已经过浏览器验收。

### 修复结果

1. **完整选择缓存过期**：主列表刷新和组刷新都会使完整选择缓存失效；有选择时重新读取当前范围，保留仍然匹配的 ID，移除已隐藏或失效的 ID。组刷新也校验未加载页，不能仅依据首屏是否变化。组选择/取消会重新读取匹配集合，不把旧 `matchingGroupMemberIds` 当作操作依据。读取失败保留原选择，但禁用批量提交，直到重新读取成功或用户重新选择。选择 ref 与 state 同步，取消、手动改选、查询变化、退出批量和卸载都会阻止晚到响应恢复旧选择。
2. **offset 分页漏选**：在现有 `/api/core/conversations` 增加 `selection_snapshot=true` 查询方式，沿用同一个用户、来源、可见性、模式和关键词谓词，由单条 SQL 返回完整根会话 ID、组 ID、置顶和整理锁信息；`selection_group_id` 可限定单组。全局/单组选择共用 `readConversationSelection`，验证 `selection_complete`，拒绝带下一页游标或缺少完整标记的响应。普通历史浏览仍分页。这样避免会话活动时间在两页之间变化造成跳过，也避免为全选逐条读取反馈计数等详情；未设置任意数量截断。
3. **未加载侧聊的归档导航**：归档响应新增 `archived_ids`，回传服务端展开的父子会话 ID。弹窗分别回传成功根会话、失败根会话和受影响家族 ID；RecordList 按家族 ID 清理列表及当前会话，计数和失败重试仍以根会话为准。同步更新 OpenAPI schema 与生成客户端。
4. **独立 fork 的单项勾选**：RecordList 仅按 `parent_conversation_id` 判断是否嵌套；只有 `fork_origin` 的独立 fork 显示为独立行并可单独勾选/取消，保留来源说明。依附侧聊继续跟随父会话，不独立选择。

未新增路由、数据库表或迁移；复用现有批量导出、删除、归档、事务、权限和整理锁校验。选择快照同时携带依附侧聊导致的家族整理锁，并保留项目会话的锁豁免。

### 实际验证

- Node 22 下运行 RecordList **73**、SidebarGroups **50**、MainLayout **37**、conversationHistory **3**、conversationSelection **5**、ArchiveConversationModal **7** 个测试，合计 **175/175 通过**。
- 新增回归覆盖审查中的四个复现场景；另覆盖首屏不变但未加载成员失效、刷新后取消/改选/切换查询/卸载、校验失败保留选择并阻止提交、失败重试、快照不完整拒绝、独立 fork 与依附侧聊边界、归档部分失败的根 ID/家族 ID 分离。
- 后端执行原审查定向命令，并增加 `TestConversationSelection` 和 `TestConversationArchiveReturns`：`common` 编译通过（该过滤条件下无测试），`conversationgroup`、`chat` 测试通过。新增用例使用隔离 SQLite，验证超过一页的完整集合、来源/组名交集、用户/任务/归档/回收站/临时会话/隐藏外部来源边界、置顶和家族锁、项目锁豁免、独立 fork 与归档家族 ID。
- Vite 生产构建通过（约 25 秒），输出至临时目录，未覆盖用户运行中的前端产物；现有大 chunk 和依赖警告仍存在。
- OpenAPI freshness 检查通过：auth、core、scan、channel-gateway 均为 fresh。
- 以同一 TypeScript 配置和依赖进行 compiler API 全量诊断对照：修复前工作区 **706** 条，修复后 **706** 条；按文件、诊断代码、消息逐项比较，无新增诊断。全量类型检查仍未通过，未将既有类型问题扩大到本次修复范围。
- `git diff --check` 通过。分支仍为 `codex/requirement-5-5`，HEAD 仍为 `3486910b9f761ae84f3f98b823bcb2e14d2500d4`，暂存区为空；未提交、未推送、未切换或重置分支。

### 验证边界

未重启本地运行环境，未对最新修复进行真实浏览器端到端复验；没有操作既有用户会话、QA55 回收站数据或永久删除数据。新快照协议需要前后端同时包含本节改动；旧后端没有完整标记时前端会拒绝全选结果。未做大规模账号的负载压测，也未以 SQLite 测试代替其他数据库的真实联调结论。

## 12. 第二轮独立审查后的修复与验证（2026-10-20）

第二轮审查用隔离反例确认了四项 P2 问题。本节记录其修复；第 11 节为上一轮结果，不能代替本轮验证。

### 修复结果

1. **后台刷新关闭批量归档弹窗、晚到结果恢复旧选择**：选择缓存失效不再直接关闭归档弹窗。无变化刷新保留已选文件夹，正在提交的批次保持进行中状态；后台校验中或失败时禁用未提交弹窗的确认操作，完整快照确认目标已失效或受整理锁保护时关闭未提交弹窗。弹窗批次版本与查询范围共同约束完成回调，查询变化、退出批量或卸载后不恢复旧失败 IDs；已成功的归档仍通过最新列表清理函数处理受影响会话和导航。当前批次的失败 IDs 在成功项清理前写入，再经完整集合重校验，避免把提交期间已隐藏的失败会话恢复成可提交选择；该边界已先复现失败再修复。
2. **早发组选择覆盖后发全局选择**：组选择发起时通过 `beginSelection` 获取与全局选择共用的意图版本，提交结果和显示错误前检查版本。用户较晚开始的选择优先，不再由早到组响应取消后发的全局全选。
3. **旧单组快照遮蔽较新的完整组 map**：本地组快照绑定当前全局 map 版本；新全局 map 到达后忽略旧组快照。较晚成功读取的组快照仍可使用。组复选框与首次取消整组因此依据最新完整 ID 集合计算，不受旧未加载成员影响；版本标记不保留整份旧全局 map。
4. **全量选择锁查询重复扫描历史快照**：改为一次聚合当前用户的活动家族锁，再连接完整选择集合。投影真实的 `family_root.id` 保留 SQLite 连接键的文本亲和性，使派生锁表使用自动覆盖索引，避免每个根会话重新扫描整个锁集合。保持一条 SQL、最小活动 run ID、依附侧聊家族锁、独立 fork、用户隔离及项目锁豁免语义。

### 实际验证

- Node 22：RecordList **82/82**、SidebarGroups **53/53**、MainLayout **37/37**、conversationHistory **3/3**、conversationSelection **5/5**、ArchiveConversationModal **7/7**，合计 **187/187 通过**。本轮新增 12 项，覆盖上述异步完成顺序、缓存优先级、归档弹窗保留、目标失效/校验失败保护及部分失败后的重新校验。
- 后端：原审查定向集合，加 `TestConversationSelection`、`TestConversationArchiveReturns`，全部通过。新增家族锁聚合回归覆盖多个活动/已完成锁、父子锁、独立/依附 fork 及用户隔离；原有范围、来源、分组和项目豁免用例继续通过。
- 隔离 SQLite handler 性能复现：根会话/快照各 1,000 条时，running/succeeded 分别约 **5.09/2.45 ms**；各 3,000 条时约 **14.63/7.92 ms**；各 10,000 条时约 **52.14/26.29 ms**。修复前对应已完成快照场景约为 **0.56 秒、5.2 秒、超过 60 秒**。万条用例检查只执行一条真实 SELECT、完整返回全部 IDs 和正确锁状态；EXPLAIN 显示物化 `selection_locks` 后通过 `AUTOMATIC COVERING INDEX (conversation_id=?)` 连接。这些是本机隔离数据库测量，不包含网络和前端渲染，也不代表生产并发压测。
- Vite 最终生产构建通过（约 **28.37 秒**），输出至 `/tmp/lazymind-req55-fix2/dist`，没有覆盖运行中的产物。现有大 chunk、Sass 和依赖警告仍存在。
- 全量 TypeScript 诊断按文件、代码、消息与本轮修复前对照，仍为 **706 条**，无新增诊断；全量类型检查本身仍未通过。
- `git diff --check` 通过；仍在原分支及原 HEAD，暂存区为空，未提交、未推送、未切换或重置分支。

### 验证边界

未重启或重置本地运行环境，未对本轮修复做真实浏览器端到端复验；未操作现有会话、QA55 回收站数据或永久删除数据。性能及 SQL 执行计划验证使用隔离 SQLite，尚未进行 PostgreSQL/MySQL 真实数据库联调或生产并发压测；没有以模拟测试结果泛化外部来源、侧聊和整理锁的全部浏览器场景。

## 13. 小范围代码简化（2026-10-20）

- 仅整理 `RecordList/index.tsx`：查询范围变化复用 `exitBatchMode`；全局全选与刷新重校验共用 `loadBatchSelection`，保留恢复选择与全选各自的失败语义；归档全部成功时直接退出，部分失败时才单独清理和写入失败集合，避免重复关闭/取消。
- 相对本轮简化前，实现文件新增 29 行、删除 49 行，净减少 **20 行**。没有新增状态或修改既有测试、后端 SQL、组缓存版本及归档批次保护。
- 原有六个相关测试文件 **187/187 通过**；全量 TypeScript 仍为 **706 条既有诊断**，按文件、代码、消息对照无新增或减少；`git diff --check` 通过。
- 本轮未重跑后端测试或生产构建，未重启环境、操作用户数据或做浏览器端到端复验；分支、HEAD 和空暂存区保持不变，未提交/推送。
