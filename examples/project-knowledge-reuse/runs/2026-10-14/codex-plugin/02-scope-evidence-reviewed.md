# 知策 R-05：范围变更表述的原文对照

> 来源链接审核后恢复（2026-10-14）：初次输出将下载路径错误拼接为网页链接；本版仅修正来源链接及其说明，保留初次文件，正文事实不变。本轮通过 LazyMind knowledge.document.list 核对文档名称与实际 ID。新网页链接由协调者浏览器验收（据本轮用户提供的验收结果）；需要本地服务和登录，不是公开链接，助手本轮未亲自打开。正文保留的检索、全文读取及工具记录属于原始写作轮次，不表示本轮重新执行。

本次核对日期：2026-10-14。通过 LazyMind 重新检索知识库 `ds_b4cbf46595d98dd2a0e872c1d96bf03d`（showcase-zhice-v1），重新读取产品方案、PRD 与评审全文；不以上轮摘要代替原文。

**结论：** R-05 指向产品方案“任务详情·概览”中的“确认计划；改动范围后须重新确认”。它漏写了启动前后的适用边界。按 PRD 已写出的拟议规则及评审的澄清建议理解：**启动前改范围须重新确认有效计划；启动后改竞品、维度或来源范围，应复制配置创建新任务。** R-05 是尚未解决的 P2 表述问题，本次说明不改变正式研发交付“退回修改 / blocked”的状态。以下“原文规则”均指草稿中写出的规则，不表示规则已获业务接受或产品已实现。（来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-4]「页面沿用户任务组织」—任务详情·概览） （来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4] 状态转换表后说明；[section-5] FR-002） （来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-05；[section-5]「结论与复验范围」）

**1. R-05 究竟指哪一句**

| 原文来源与位置 | 原文摘录 | 证据含义 |
|---|---|---|
| 产品方案 design.txt，[section-4]「页面沿用户任务组织」，表格“任务详情·概览”行的“主要操作与状态”列 | “确认计划；改动范围后须重新确认” | R-05 指出的具体表述；同一行展示问题、对象、比较维度、来源范围，但没有限定变更发生的阶段 |
| PRD prd.txt，[section-4]「对象、字段与状态保持同一含义」，任务状态转换表之后 | “计划启动后如要变更竞品、维度或来源范围，应复制配置创建新任务，不在原运行中静默替换。” | 明确启动后的变更对象与处理方式 |
| PRD prd.txt，[section-5]「功能需求与可观察反馈」，FR-002“确认研究范围” | “明示竞品、维度、来源范围；确认后锁定版本并进入执行中，展示步骤进度” | 确认有效计划后锁定版本并启动，构成前后阶段的分界 |
| 同一 FR-002 的“异常、权限与恢复”列 | “未确认不访问外部资料；运行中改范围需新建任务；重复确认不生成重复运行” | 再次明确：运行中改范围不能仅在原任务上重确认 |

产品方案上述原句的文档 ID 为 `doc_dbd6a8d75dba652c433795dee8d095cc`；PRD 三处原句的文档 ID 为 `doc_bc9879c116ae6e5b264200dc3f09378b`。（来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-4] 任务详情·概览） （来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4] 表后说明；[section-5] FR-002）

**评审原文：** review.txt 的 [section-3]「可定位的发现」中，R-05 / P2 写道：

> 位置与证据：方案 §3 写“改动范围后须重新确认”，PRD §3 写启动后改范围需新任务  
> 影响：脱离上下文阅读时可能把执行中改范围理解为原任务重确认  
> 最小修复与建议负责角色：产品在方案该处补一句“启动前重确认；启动后创建新任务”  
> 复验方式：检查计划页及执行中改范围文案与 PRD 一致

（来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-05 / P2，文档 ID doc_46a23b2fb47ba47f3b8a9ef0195cc54a）

**定位差异：** 评审保留的是“方案 §3 / PRD §3”交叉引用；本次读取的提取文本中，目标原句实际都位于 [section-4]，PRD 另在 [section-5] FR-002 重申。本文按实际锚点、标题和条目定位。两套编号不一致的原因本次未验证，不能声称已经修正原始文档编号。依据即上表与 R-05 原句。

**2. 启动前与启动后分别如何处理**

| 阶段 | 原文规则或建议 | 我的解释及其边界 |
|---|---|---|
| 启动前、计划待确认 | 产品方案要求变更后重确认；评审建议明确补上“启动前重确认”；PRD 要求未确认不访问外部资料，确认后锁定版本并进入执行中 | 范围调整后，应让创建者确认调整后的有效计划，再启动研究。这是结合三份文本的解释；没有证据表明存在额外的“已确认但尚未启动”独立状态，也不据此新增该状态 |
| 已启动后要变更竞品、维度或来源范围 | PRD 明确要求“复制配置创建新任务”，运行中改范围需新建任务 | 新范围应由新任务承载，并走新任务的计划确认；不能把“重新确认”理解为覆盖原运行计划。原任务是否同时暂停或取消，三份原文没有规定自动联动，不能自行补成必须取消旧任务 |
| 已暂停继续、执行失败重试 | PRD 状态表分别要求“创建者继续，范围不变”和“同范围恢复未完成部分”；产品方案写继续时“按同一计划恢复未完成部分” | 暂停或失败不等于回到启动前，也不是在原任务中改范围的例外；若要变更范围，仍应按启动后新建任务理解 |

本表原文依据：（来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-3] 状态转换表“已暂停”；[section-4] 任务详情·概览） （来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4] 待确认、已暂停、执行失败转换及表后说明；[section-5] FR-001、FR-002） （来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-05 最小修复建议）

**3. 为什么当前写法容易误读**

**原文指出的风险**是“执行中改范围”被理解为“原任务重确认”。“改动范围后须重新确认”单独看只限定了变更与确认的先后顺序，没有说明它只适用于启动前，也没有写启动后的新任务要求。（来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-05 影响） （来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-4] 任务详情·概览）

**我的解释：** 读者只看到概览页操作说明时，可能合理但错误地推导出“在同一个任务里改竞品 → 再点确认 → 继续研究”。这会偏离 PRD 的“确认后锁定版本”和“启动后复制配置创建新任务”要求，让既有证据、执行进度与研究范围的对应关系变得含混。后半句是对设计风险的解释，不是已经观察到的系统故障。产品方案自身也写了确认后“计划形成固定版本”，所以 R-05 更适合理解为阶段条件未写清，而不是已获接受的两套相反产品决定。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4] 待确认转换及表后说明；[section-5] FR-002） （来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-3] 状态转换表“待确认”） （来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-05 / P2）

还需区分**研究范围变更**与**已确认报告修订**：PRD 允许对已完成任务发起报告修改，新增报告草稿、保留旧确认版本；该规则不授予在原研究运行中改竞品、维度或来源范围的权限。这是对两类规则对象的区分，不是新增规则。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4]「已完成 → 待复核」及表后说明；[section-5] FR-009）

**4. 尚未接受的决定与交付阻塞状态**

**原文的建议，尚非接受记录：** “启动前重确认；启动后创建新任务”是 review.txt 对产品角色提出的最小修复建议；评审要求后续检查计划页和执行中改范围文案与 PRD 一致。design.txt 标记为“案例方案，待确认”，PRD 标记为“示例草稿，非已批准需求”，并说明补全规则尚未确认。因此，这份证据说明不能充当业务批准、文案修复或测试通过凭据。（来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-05） （来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-8]「版本、案例性质与使用边界」） （来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-9]「升级为正式需求前仍需确认」；[section-10] 版本声明）

**原文的当前状态：**

> 本次结论为“退回修改”，适用目标是正式研发交付。因为存在未解决 P1，不使用“有条件通过”来暗示可先进入正式实施。

> 当前五项均为 not-resolved。未收到真实负责人确认、未修改上游文件、未执行正式复验；后续阶段只提供 blocked 的交付整理示例。

（来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-5]「结论与复验范围」）

**我的判断：R-05 的澄清不改变当前阻塞状态。** 本次仅解释原文，没有关闭 R-05；即使未来只完成 R-05 的修复与复验，也不能据此解除以下三项 P1：

| P1 | 原文记载的未解决门禁 |
|---|---|
| R-01 | 范围、角色、版本等核心规则没有正式接受的需求决策基线 |
| R-02 | 数据保留、删除及敏感材料边界未定 |
| R-03 | 技术可行性及性能、可用性、大小、成本等目标未验证或未明确 |

这三项是 review 明确记载的就绪阻塞。评审给出的顺序是先接受或重开核心规则，再补齐数据政策和技术验证，同时澄清 R-05，最后按范围处理原型缺口；不能把一个 P2 的文字澄清等同于正式交付获准。（来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-01–R-03；[section-5] 处理顺序及结论）

**5. 本次实际检索、读取与限制**

使用 `knowledge.search` 对同一知识库执行两次检索，均设置 top_k=8：

- “R-05 改动范围后须重新确认 启动前 启动后 新任务”：返回 5 个命中，涉及 review.txt、design.txt、handoff.txt。
- “确认研究范围 锁定计划版本 运行中 改范围 正式研发交付 P1”：返回 5 个命中，涉及 design.txt、prd.txt、review.txt。

随后用 `knowledge.document.get` 重新读取以下三份文档，均请求正文及 chunks；正文全部 `truncated=false`，chunks 全部返回且无后续页：

| 文件 | 实际文档 ID | 本次读取范围 | 来源网页链接（协调者浏览器验收） |
| design.txt | `doc_dbd6a8d75dba652c433795dee8d095cc` | 全文及 3 个 chunks | [打开 design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc) |
| prd.txt | `doc_bc9879c116ae6e5b264200dc3f09378b` | 全文及 4 个 chunks | [打开 prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b) |
| review.txt | `doc_46a23b2fb47ba47f3b8a9ef0195cc54a` | 全文及 2 个 chunks | [打开 review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a) |

以上来源网页链接已按核对的知识库 ID 和文档 ID 修正，由协调者浏览器验收；需要本地服务和登录，不是公开链接，助手本轮未亲自打开。检索还返回 handoff.txt（`doc_fee089df8b540192144ac3cf56ac4cec`）的片段，本轮没有对它调用全文读取，以上结论不依赖它或上轮摘要。

指定检索、三份原文读取与证据说明写作均已完成，没有失败或缺页。没有读取本地仓库材料、访问外部网页或运行 Workflow。原始 HTML/Markdown、原型及界面验证记录没有在本轮读取或执行；没有修改上游文案、确认业务决定或执行正式复验。R-05 及正式交付状态均沿用原文的未解决记录。

