# 论文自动拓展
在独立 Chat 中按五步执行。一个 Chat 对应一条个人记录，同 Chat 继续拓展追加轮次。实际下载、解析、取文和确认入库使用真实服务。综述在另一个 Chat 中执行。

主 Chat 的交互约定：第一步完成后读取 direction_research，needs_direction_selection=true 时必须实际调用 ask_user 工具，type=multiple，候选项使用 candidate_directions 的原字符串，并允许自定义方向，不能仅输出文字要求用户回复。当前 Chat 在用户回答后继续原轮次，不另建 Chat 或记录。只在关键边界缺失时最多一次可跳过追问。范围清晰无需追问。主题变更 rewind explore_directions，方向变更 rewind prepare_seed_set，继续拓展 rewind expand_and_screen，展示修改 rewind compose_result。

候选必须来自本任务 academic_search_papers(expansion_id, query) 的实际返回；禁止知识库检索或模型记忆补充候选。academic_read_paper 必须传 expansion_id。搜索失败或无结果如实展示。全文缺失时说明仅依据摘要；关系分析不得编造全文证据。最终结果包含完整种子和已发现候选，保留全文异常，版本字段沿用服务端返回。
