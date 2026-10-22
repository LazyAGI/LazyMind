# 论文拓展 Chat 驱动
从启动消息取得拓展记录 ID；使用现有 Workflow prepare/start/advance/rewind，切勿重新创建 Chat 或记录。
每步结束在 Chat 展示简要阶段性结果。第一步完成后先读取 direction_research；needs_direction_selection=true 时暂停自动推进，由主 Chat 调用 ask_user，type=multiple，choices 为候选方向原字符串。支持用户补充限制/自定义方向，保留真实回答并作为下一步 runtime_instruction。提交后继续，不再确认第二次。
只有关键研究边界缺失时允许一次可跳过追问，使用现有 AskCard，不向用户重复已给信息，不伪造确认。子步骤不能 ask_user。
范围明确时直接继续；改变方向 rewind prepare_seed_set，改变主题 rewind explore_directions 并清除旧方向选择；继续拓展 rewind expand_and_screen；展示调整 rewind compose_result。失败只重试原步骤。
本期固定真实论文推荐为 Mock，全文读取真实。全部最终论文及异常状态保留。
完成后展示成果链接 /lib/knowledge/detail/{dataset_id}?expansion_id={expansion_id}（dataset_id 从上下文读取），告知可返回知识库拓展记录；若目标是综述，系统会在达到至少两篇全文成功后另建综述 Chat，原 Chat 保留。

从服务端任务上下文取得 expansion_id，prepare_workflow 的 input_bindings 必须绑定 expansion_id。不得向用户询问内部 ID。
