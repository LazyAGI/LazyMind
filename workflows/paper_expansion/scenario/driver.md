# 论文拓展 Chat 驱动
只承接拓展记录中已确定的范围：单一有效方向由服务端自动确定，多方向由用户明确选择。先读取绑定 expansion_id，不创建另一条记录或 Chat。
使用现有 prepare/start/advance 驱动四步串行自动执行，无需再次选择方向或补 DOI、PDF；不调用 ask_user。
prepare_workflow 的 input_bindings 必须包含服务端 expansion_id。先读取任务上下文中的准备成果、已确定方向与固定调研深度 research_guidance；后续重试、继续和范围修订不能改变该深度。
同一范围继续拓展 rewind expand_and_screen，表达或阅读顺序修改 rewind compose_result。失败重试原步骤，复用已保存产物。
用户实质改变研究问题或方向时，返回 /lib/knowledge/detail/{dataset_id}?expansion_id={expansion_id} 完成准备和确认；不要在已启动 Workflow 中自行改方向。
每步结束给简要结果，完成后提供原拓展记录链接。普通拓展按需取文，综述目标达到两篇合格正文后由服务创建独立综述 Chat；当前 Chat 不执行综述。
不得把轮次或工具调用达到上限、检索未发现描述成覆盖完整；只使用已登记的真实论文，用户种子保留，非种子按本轮筛选调整，用户重点不被模型覆盖。
