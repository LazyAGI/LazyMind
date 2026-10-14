# 知策方案摘要｜新成员阅读版

> 来源链接审核后恢复（2026-10-14）：初次输出将下载路径错误拼接为网页链接；本版仅修正来源链接及其说明，保留初次文件，正文事实不变。本轮通过 LazyMind knowledge.document.list 核对文档名称与实际 ID。新网页链接由协调者浏览器验收（据本轮用户提供的验收结果）；需要本地服务和登录，不是公开链接，助手本轮未亲自打开。正文保留的检索、全文读取及工具记录属于原始写作轮次，不表示本轮重新执行。

编写日期：2026-10-14。材料范围：LazyMind 知识库 `showcase-zhice-v1`（`ds_b4cbf46595d98dd2a0e872c1d96bf03d`）中的四份固定示例文档。正文由本次助手撰写，LazyMind 仅用于列举、检索与读取。

**先明确材料性质。** 知策是面向产品团队的虚构竞品研究 Agent，核心设想是把研究任务、证据、结论和报告版本关联起来，让产品经理研究、负责人复核、成员读取确认结果。当前材料可以用于理解和讨论方案，但产品方案与 PRD 尚未批准，面向正式研发交付的评审结论为“退回修改”，交付包为 `draft / engineering_readiness: blocked`。（来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-8]「版本、案例性质与使用边界」） （来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-5]「结论与复验范围」） （来源：[handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec)，`doc_fee089df8b540192144ac3cf56ac4cec`，开篇状态声明）

本文区分四种证据状态：

- **案例设定**：目标用户、使用场景与假设中的原有痛点，不等于真实用户调研结果。
- **提议规则**：文档规定的拟议行为，尚不代表业务接受、产品实现或测试通过。
- **已验证事实（限本次文档核对）**：本次实际读取的文本、条目、版本声明与状态记录。材料自述的界面验证仅作转述，本次未独立验证。
- **未知或待验证**：未提供证据、未执行测试、未确认决定或存在歧义的事项。四份材料开篇均声明“不是已验证、已批准或已实现的产品事实”。

**一、用户与任务**

**案例设定：** 主要用户是产品经理、产品负责人和团队只读成员。核心任务包括每周竞品研究、新功能专题对比及负责人复核；希望减少来源与结论分离、冲突被静默取舍、审核后继续覆盖同一文件造成的重复核验。周报由人手动发起，可以复制上次配置，但必须重新核验来源，不能把旧日期材料当作新事实。付费意愿、真实研究频率、复核价值和操作负担仍待验证。（来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-2]「首版围绕一份可复核的竞品周报」、[section-6]「验证产品是否值得继续做」）

**提议范围：** 首版围绕公开资料研究、计划确认、证据与冲突、执行控制、报告编辑复核、版本保留、导出与归档。明确不含自动定时研究、登录或付费网页、企业知识库、自动发送、公开链接分享、外部系统写入、自动变更路线图、永久删除入口、技术架构及上线排期。这里的“企业知识库不在范围内”是知策产品提议范围，与本次使用 LazyMind 知识库读取材料是两回事。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-2]「目标、范围与非目标」）

**二、主要流程与证据要求**

下列五步均为**提议规则**：

1. **提出问题。** 创建者填写研究问题、自家产品说明、至少一个竞品和一个比较维度；竞品输入槽默认三个，可调整。校验必填与 URL 格式后形成待确认任务；输入错误保留其他内容，重复提交不重复创建。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-5] FR-001；[section-7] AC-001、AC-002、AC-013）
2. **确认计划。** 创建者确认竞品、维度和来源范围，锁定计划版本后才启动外部研究。PRD 明确：启动后改范围须复制配置新建任务。方案中“改动范围后须重新确认”的表述仍有歧义，见下文 R-05，尚不能视为已统一批准的规则。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4] 状态转换表后说明；[section-5] FR-002） （来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-4]「任务详情·概览」） （来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-05）
3. **收集证据。** 仅访问公开资料，记录链接或材料标识、标题、发布方、片段、访问日期和适用版本/日期。来源需要登录、付费或受权限限制时跳过并提示；网页、附件中的指令不能成为操作授权。局部失败可保留成功材料并标未知，整体阻塞进入执行失败。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4]「证据」；[section-5] FR-003、FR-005）
4. **复核结论。** 报告包含摘要、比较矩阵、证据、建议与缺口，区分官方说明、分析推断、产品建议和未知。冲突来源并列呈现日期/版本；无支持材料的事实要补齐、移除或降为假设，不能作为已核实结论提交。排除证据保留理由和历史引用，但被排除的唯一证据不能继续支撑已核实主张。草稿提交通过检查后进入待复核，创建者停止原地编辑，需负责人退回才能修改。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4]「结论」；[section-5] FR-004、FR-006、FR-007；[section-7] AC-015）
5. **确认与导出。** 负责人确认确切版本并留下人、时间与版本记录；可确认带显式缺口的报告，但须确认知晓缺口。退回必须填写理由；过期版本确认应拒绝。负责人确认不构成事实绝对正确的认证，也不触发自动对外发送。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-5] FR-008、FR-011；[section-7] AC-017）

**提议状态规则：** 任务主路径是“待确认 → 执行中 → 待复核 → 已完成”，报告另有“草稿 → 待复核 → 已确认”。系统生成报告并完成基础校验可进入待复核；负责人退回时任务仍待复核、报告变草稿。暂停停止派发新操作；继续或失败重试只恢复未完成部分。取消后保留已有内容，但不能恢复原任务；在途结果不能触发后续动作。归档只是独立可见性标记，不是执行状态，也不是删除。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4] 对象与状态转换表；[section-5] FR-005、FR-012）

**三、角色边界**

下表全部属于**待业务确认的权限提议**，角色由宿主提供；每次读取、编辑、确认和导出都要校验团队边界，不能仅靠隐藏按钮。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-3]「三类角色的权限不互相替代」；[section-5] FR-010）

| 角色 | 拟议权限 | 关键限制 |
|---|---|---|
| 产品经理 | 创建和启动任务；读取、编辑、补证据及控制自己创建的任务；导出自己的草稿/部分结果；归档或恢复自己的非执行中任务 | 不能自审；不能据此编辑他人任务 |
| 产品负责人 | 读取本团队全部任务的草稿、证据和过程；确认或退回具体报告版本；导出本团队草稿/部分结果 | 本角色不提供创建启动、归档恢复权限，也不能代创建者修改报告 |
| 只读成员 | 在本团队内读取和导出已确认报告 | 不得读取草稿、证据和执行过程，不可编辑或确认 |

三类角色都可以读取和导出本团队已确认报告。上述限制没有生产权限测试通过证据；交付包只记载前端角色模拟，服务器团队隔离未测。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-3] 权限表） （来源：[handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec)，`doc_fee089df8b540192144ac3cf56ac4cec`，[section-3] FR-010 覆盖说明）

**四、版本、导出与归档**

- **报告版本提议：** 已确认报告不可原地覆盖。修改时生成新草稿，任务重新进入待复核，旧版本、原证据与确认记录保留；再次确认须重新复核。保存冲突时拒绝静默覆盖，保留未提交文本，重新载入后人工合并。证据片段不是完整网页存档，来源失效时须提示无法重新访问，不得声称完成最新核验。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4] 报告版本及修订转换；[section-5] FR-009） （来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-3] 证据保留说明）
- **导出提议：** 有权用户选择具体版本及 HTML/Markdown；文件必须包含版本、状态、观察日期、来源和缺口，草稿或部分结果须明显标注。导出前再次校验版本和权限，失败可重试且不改变报告状态；通知仅站内展示，不发邮件、不推送外部应用。原型覆盖记录只有本地 Markdown 示例下载，生产导出未测，不能据此认定两种格式已实现。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-5] FR-011；[section-6] 通知规则） （来源：[handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec)，`doc_fee089df8b540192144ac3cf56ac4cec`，[section-3] FR-011 覆盖说明）
- **归档提议：** 创建者只能归档/恢复自己的非执行中任务；归档隐藏默认列表条目，报告及引用不变。首版不提供永久删除入口，但保留期限、删除机制和敏感材料政策仍未确定；这不是允许永久保留真实数据的决定。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-3] 权限表；[section-5] FR-012；[section-9] 数据政策未决项）
- **交付文件基线：** 交付包列产品方案内容 v1.0、PRD 内容 v1.0、概念原型 v2.0、评审 v2.0及交付包 v2.0。它声称用版本清单和 SHA-256 固定示例文件集合，并要求上游实质修改形成新版本及重新评审；校验和不代表批准。本次未读取版本清单或原型文件，未复核这些文件的实际校验和。（来源：[handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec)，`doc_fee089df8b540192144ac3cf56ac4cec`，[section-1]「产物索引与版本基线」）

**五、当前评审与交付状态**

**已核对的文档事实：** PRD 实际列出 12 项功能需求（FR-001–012）与 18 项验收设计（AC-001–018），并明确验收尚未执行。评审 v2.0 记载三个 P1 就绪阻塞、两个 P2 问题，五项均为 `not-resolved`；结论是面向正式研发交付“退回修改”，没有使用“有条件通过”。（来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-5] 功能需求表；[section-7] 验收表及开篇声明） （来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，开篇；[section-5]「结论与复验范围」）

下表是**材料所记载的问题与建议关闭条件**，不是本次新完成的复验，也没有已指定个人负责人或期限。（来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-01–R-05） （来源：[handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec)，`doc_fee089df8b540192144ac3cf56ac4cec`，[section-5] OPEN-01–OPEN-05）

| 问题 | 级别与现状 | 建议关闭条件及角色 |
|---|---|---|
| R-01 核心规则仍为提议 | P1，阻塞正式交付 | 产品＋业务接受或重开范围、角色与核心机制，记录采用版本 |
| R-02 数据生命周期未定 | P1，阻塞正式交付 | 业务＋安全确认保留、删除、敏感材料和授权规则及验收 |
| R-03 技术可行性与目标未验证 | P1，阻塞正式交付 | 研发＋产品验证允许来源、失败边界、性能与成本，并形成有依据的目标 |
| R-04 原型覆盖不完整 | P2，具体是否阻塞取决于最终实施范围 | 设计＋测试明确哪些路径由规格说明、哪些须补原型，区分演示覆盖和验收通过 |
| R-05 启动前后改范围表述歧义 | P2，应在冻结前修正 | 产品澄清“启动前重确认；启动后创建新任务”，使方案与 PRD 一致 |

**材料自述的验证边界：** 评审称已核对静态文档及原型实现范围、附有界面验证记录；本次未读取该记录或运行原型。交付覆盖表仍列真实抓取、持久化与幂等、事实校验、过期请求、多人并发、服务端团队隔离和生产导出等未测，FR-012 归档只有文档规则、原型未实现。真实用户测试、生产权限渗透测试、研究质量对照和全部 AC 端到端测试没有完成记录。（来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-1] 评审范围；[section-2] 设计基础） （来源：[handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec)，`doc_fee089df8b540192144ac3cf56ac4cec`，[section-3] FR-001–012 覆盖表）

**交付仍被阻塞：** DEC-01–05 只是提议索引分组，全部 `proposed`；产品、设计、研发、测试、业务与安全的确认均未完成。真实模型/采集服务、宿主身份和团队权限依赖尚未选定和验证。需先关闭阻塞、接受上游决定并针对性复验，再重新判断就绪度；目前没有开工、上线、架构、估时或排期承诺。（来源：[handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec)，`doc_fee089df8b540192144ac3cf56ac4cec`，开篇；[section-3] DEC-01–05；[section-5] DEP-01；[section-6]「交付确认清单」）

**六、不一致与未知应如何理解**

- **范围变更歧义尚未修正。** design 的“改动范围后须重新确认”与 PRD 对启动后新建任务的明确限制，需要按 R-05 澄清。本摘要并列呈现，不把建议修复写成已批准决定。（来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-4]「任务详情·概览」） （来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-4] 状态转换表后说明） （来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-05；[section-5] 五项未解决状态）
- **交叉引用编号与提取文本锚点不一致。** 例如 review 的 R-05 写“方案 §3 / PRD §3”，相关文字实际位于本次 design 的 [section-4] 和 PRD 的 [section-4]；handoff 的权限“PRD §2”对应本次 [section-3]，其数据约束“§7”对应本次 [section-8]。可能涉及原版编号与提取锚点差异，但原因未验证；本文统一采用实际读到的锚点、标题及条目 ID，不将旧编号当作已校正。（来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-3] R-01、R-05） （来源：[handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec)，`doc_fee089df8b540192144ac3cf56ac4cec`，[section-2] 规格来源索引） （来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-3]、[section-8]、[section-9]） （来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-4]）
- **不同文档的就绪声明有阶段差异。** design 与 PRD 写“研发就绪度未评估”；review 和 handoff 提供后续评审/交付阶段的“退回修改 / blocked”记录。应保留各自阶段含义，不能把后续整理解释为上游已获批准。（来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-8]） （来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-10]） （来源：[review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a)，`doc_46a23b2fb47ba47f3b8a9ef0195cc54a`，[section-5]） （来源：[handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec)，`doc_fee089df8b540192144ac3cf56ac4cec`，[section-6]）
- **价值与效果未知。** 四项拟议指标为可追溯报告完成率、有效证据覆盖率、人工纠错率和审核耗时，均无真实基线或达成数值。是否需要持续周报、是否必须分离编辑与复核、用户愿付成本、来源失败容忍范围及可接受速度均待验证。“存在引用”不能等同准确，“官方说明”不能等同实际性能验证。（来源：[design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc)，`doc_dbd6a8d75dba652c433795dee8d095cc`，[section-5] 证据判断；[section-6] 指标与待确认问题） （来源：[prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b)，`doc_bc9879c116ae6e5b264200dc3f09378b`，[section-8]、[section-9]）

**七、实际检索、读取清单与未完成步骤**

首先调用 `knowledge.document.list` 列出指定库全部四份文件（total=4，无后续页），再用 `knowledge.list` 核对库名和 ID；库说明为“仅用于插件知识库复用验收，非已批准产品事实”。随后执行两次限定该库的 `knowledge.search`（均 top_k=12）：

| 检索词（原样） | 实际返回 |
|---|---|
| 知策 用户 任务 主要流程 角色 权限 边界 | 4 个命中：prd.txt 2 个、review.txt 1 个、design.txt 1 个 |
| 知策 版本 导出 评审 交付 状态 待确认 | 5 个命中：prd.txt 4 个、handoff.txt 1 个 |

然后对下列四份文档分别调用 `knowledge.document.get`，启用 `include_content=true`、`include_chunks=true`、chunk 页大小 100。全部正文返回 `truncated=false`，全部 chunk 已取回，无后续页；目录元数据虽显示 `parse_status=NONE`，本次读取实际返回了正文和 chunks，摘要依据正文而非目录元数据。

| 实际检索命中且全文读取的文件 | 文档 ID | 正文范围 / chunks | 来源网页链接（协调者浏览器验收） |
|---|---|---|---|
| design.txt | `doc_dbd6a8d75dba652c433795dee8d095cc` | 开篇及全部章节 / 3 | [打开 design.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_dbd6a8d75dba652c433795dee8d095cc) |
| prd.txt | `doc_bc9879c116ae6e5b264200dc3f09378b` | 开篇及全部章节 / 4 | [打开 prd.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_bc9879c116ae6e5b264200dc3f09378b) |
| review.txt | `doc_46a23b2fb47ba47f3b8a9ef0195cc54a` | 开篇及全部章节 / 2 | [打开 review.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_46a23b2fb47ba47f3b8a9ef0195cc54a) |
| handoff.txt | `doc_fee089df8b540192144ac3cf56ac4cec` | 开篇及全部章节 / 2 | [打开 handoff.txt](http://127.0.0.1:8091/lib/knowledge/knowledge/ds_b4cbf46595d98dd2a0e872c1d96bf03d/doc_fee089df8b540192144ac3cf56ac4cec) |

以上来源网页链接已按核对的知识库 ID 和文档 ID 修正，由协调者浏览器验收；需要本地服务和登录，不是公开链接，助手本轮未亲自打开。四份文件均标记同一来源仓库修订 `84a1a5b7f1d1e9ef59022f2c57ab7dfe1fd54ac2`，这是文件头提供的溯源信息，本次没有读取本地仓库或核对该修订。导入更新时间为 2026-10-14，不应据此推断产品状态在当日已经更新或批准。

**未能完成的验证及未读取材料：**

- 指定的列举、检索、四份全文读取与摘要写作没有失败或缺页步骤。本次仅能确认这四份材料中的记录，无法独立确认生产实现、批准记录或真实测试结果。
- 材料提到的《00-案例背景.md》、方向说明、竞品与生态位报告、原始产品方案/PRD/评审 Markdown 或 HTML、交互原型说明及单文件 HTML、界面验证记录、版本清单.json 和图示资源均未在本次作为独立文件提供或读取；四份 txt 对它们的转述不等于本次读过原件，也不能据此验证校验和、画面或测试结果。
- design.txt [section-7] 引用的 [Perplexity](https://www.perplexity.ai/help-center/en/articles/13600190-what-s-new-in-advanced-deep-research)、[Manus](https://help.manus.im/en/articles/12135428-how-can-i-use-manus-collab)、[Dify](https://docs.dify.ai/en/cloud/use-dify/nodes/human-input) 外部页面，本次均未访问，仅保留材料中的链接，不作为独立已读取证据。
- 按请求没有从本地仓库读取方案材料、没有联网补充、没有运行 Workflow、没有执行产品验证或改动上游文档。本次写作不改变任何评审、审批或交付状态。

