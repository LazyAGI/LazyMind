# artifact_runtime / artifact_flow 使用教程

本文档记录当前 `LazyRAG/evo` 内新 artifact runtime 和 artifact flow 的真实使用方式，后续迁移旧 evo operation 或开发新能力时以本文为准。

核心原则只有一句：

> Evo 是 artifact-centric：系统通过 artifact 是否存在、是否为当前 effective ref 来推进流程；operation 只是产物生成函数，不是流程状态机。

不要按传统 workflow engine 的方式给每个 step 维护一套业务状态。当前实现里，Graph 只看 `effective_artifacts`，Flow 只负责命令、暂停和放行，Store 只负责产物版本事实。

## 1. 当前目录边界

```text
evo/
  artifact_runtime/
    kernel/          # 最小核心：artifact、graph、runtime、store、materializer
    evo/             # Evo 领域薄封装：产物目录、默认 op 图、action/use case
  artifact_flow/     # 流程命令层：continue/pause/resume/cancel/retry/checkpoint
  operations/        # 业务 operation：dataset/eval/analysis/... 的纯业务函数和 materializer
  service/           # 当前 HTTP 入口；只保留健康检查，不承载旧 API 兼容层
```

边界：

- `artifact_runtime.kernel` 不知道 evo 业务。
- `artifact_runtime.evo` 知道 evo 的 artifact 名称、步骤、默认 op 图，但不做 HTTP、不做 UI、不做 message intent。
- `artifact_flow` 负责用户命令如何推动/暂停 runtime，不生成业务产物。
- `operations` 负责真实业务计算，输入是普通 dict/tuple，输出也是普通 dict。
- `service/api.py` 当前只保留健康检查；后续如需 HTTP 层，应接到 `FlowService`，而不是绕开 flow 直接操作 store。

## 2. Store：产物事实源

实现：`artifact_runtime/kernel/store.py`

当前只有 `SQLiteArtifactStore`，不再保留 InMemory store。

初始化：

```python
from evo.artifact_runtime.kernel import SQLiteArtifactStore

store = SQLiteArtifactStore('/path/to/evo-data')
```

存储结构：

```text
/path/to/evo-data/
  artifact_store.sqlite3
  payloads/
    <run_hash_prefix>/<run_hash>/<key_hash_prefix>/<key_hash>/<version_bucket>/<version>.pkl
```

SQLite 保存 record/head/event/idempotency/claim；payload 文件保存 pickle 后的真实产物。目录按 run hash、artifact key hash、version bucket 分散，避免单目录无限膨胀。

精确规则：

- `run_hash = sha256(run_id)`
- `key_hash = sha256(artifact_id + "\0" + partition)`
- run/key 目录都使用 hash 前 2 位作为一级前缀。
- `version_bucket = version // 1000`
- payload 文件名是 `<version>.pkl`

版本号由 SQLite 表 `artifact_versions(artifact_id, partition)` 分配，是 artifact key 级别的全局单调版本，不按 `run_id` 单独计数。`delete_run(...)` 不会重置版本计数。

核心语义：

- `commit_external(...)` 写入外部种子产物或用户编辑后的产物。
- `commit_outputs(...)` 写入 operation 输出。
- `effective_artifacts(run_id)` 返回当前有效产物视图。
- `invalidate(...)` 删除 head，使产物退出 effective snapshot，但历史 record/payload 仍保留。
- `delete_artifacts(...)` 物理删除指定 key/ref 的记录和 payload。传 `keys` 会删除该 run 下该 key 的所有历史版本；传 `refs` 才是删除指定版本。
- `delete_run(...)` 物理删除该 run 的 artifact records/events/idempotency/materialization_claims/payload，并通过外键清理 heads；不会删除 `artifact_versions`，也不会删除同库里的 `flow_gates` / `flow_command_receipts`。
- `events_since(...)` 只用于观察 artifact 事实事件。

重要：当前 store 没有显式 `stale` 字段。stale 是通过 `artifact_heads` 与 provenance 推导出来的：

- 某个 key 当前 head 是它的候选版本。
- op_output 的 `input_refs` 必须仍然等于对应 key 的 effective ref。
- 如果上游 ref 被替换或 invalidated，下游旧产物自动不再进入 effective snapshot。

`effective_artifacts(...)` 是计算出来的视图，不会物理清理下游 stale heads/records/payload。上游替换或 invalidate 后，下游 head 仍可能留在表里，只是在 effective 计算时被 provenance 递归过滤。

因此后续不要新增一套 step status 来判断产物是否有效。

## 3. ArtifactKey / ArtifactRef

实现：`artifact_runtime/kernel/artifact.py`

```python
from evo.artifact_runtime.kernel import ArtifactKey, ArtifactRef

key = ArtifactKey('eval.case', 'case_0001')
ref = ArtifactRef(key, 1)
```

规则：

- `ArtifactKey(artifact_id, partition)` 是 artifact 身份。
- unpartitioned artifact 的 partition 是空字符串。
- partition 是身份的一部分，`eval.case[case_0001]` 和 `eval.case[case_0002]` 独立版本。
- `ArtifactRef` 指向不可变版本。

## 4. Graph：从产物缺口推导下一步

实现：`artifact_runtime/kernel/graph.py`

Graph 不保存流程状态，不保存执行历史，只做一件事：

```python
next_ops = graph.next_ops(store.effective_artifacts(run_id))
```

它根据当前 effective artifacts 判断哪些 op 的输出缺失且输入已满足，然后返回 `NextOp`。

定义 op 时只声明输入产物和输出产物：

```python
from evo.artifact_runtime.kernel import ArtifactInput, ArtifactOutput, FixedOp

class LoadCorpus(FixedOp):
    op_id = 'dataset.load_corpus'
    inputs = {'source_config': ArtifactInput('corpus.source_config')}
    outputs = {'report': ArtifactOutput('corpus.report')}
```

不允许在 op 上写 `depends_on`。依赖关系只来自 artifact 输入输出。

### Partition

实现：`artifact_runtime/kernel/partition.py`

常用三种映射：

- `same_partition()`：同 case 输入生成同 case 输出。
- `unpartitioned_to_all()`：全局配置输入给所有 case。
- `all_to_unpartitioned()`：多个 case 产物汇总成全局产物。

`ArtifactInput` 默认是 `Unpartitioned()`。只要输入是 partitioned artifact，就必须显式写 `partition_spec=partitions`；否则 `same_partition()` 会按 unpartitioned 输入校验，graph 编译会失败。`same_partition()` 要求上下游 partition spec 兼容。

示例：

```python
from evo.artifact_runtime.kernel import (
    ArtifactInput,
    ArtifactOutput,
    FixedOp,
    StaticPartitions,
    all_to_unpartitioned,
    unpartitioned_to_all,
)

partitions = StaticPartitions(('case_0001', 'case_0002'))

class GenerateCase(FixedOp):
    op_id = 'dataset.generate_case'
    inputs = {
        'config': ArtifactInput('run.config', partition_mapping=unpartitioned_to_all()),
        'snapshot': ArtifactInput('corpus.snapshot', partition_mapping=unpartitioned_to_all()),
    }
    outputs = {
        'case': ArtifactOutput('eval.case', partitions),
    }

class AssembleDataset(FixedOp):
    op_id = 'dataset.assemble'
    inputs = {
        'cases': ArtifactInput(
            'eval.case',
            partition_spec=partitions,
            partition_mapping=all_to_unpartitioned(),
        )
    }
    outputs = {'dataset': ArtifactOutput('eval.dataset')}
```

`all_to_unpartitioned()` 的 materializer 会收到 tuple 输入。tuple 顺序来自 `StaticPartitions` 归一化后的顺序；`StaticPartitions` 会去重并排序，不保留调用方原始顺序。

## 5. Materializer：operation 的薄适配层

实现：`artifact_runtime/kernel/materializer.py`

Materializer 是同步函数：

```python
Materializer = Callable[[MaterializerContext, Mapping[str, object]], Mapping[str, object]]
```

它只做三件事：

1. 校验 runtime 传入的输入形状。
2. 调用 `operations/...` 下的业务函数。
3. 返回 output name 到 output value 的 mapping。

返回的 output name 必须和 `FixedOp.outputs` 的 key 完全一致，否则 runtime 会报 `MaterializerContractError`。

partitioned op 的运行实例 `NextOp.op_id` / `ctx.op_id` 会带 `[partition]` 后缀，例如 `eval.rag_answer[case_0001]`；materializer map 的 key 仍然是基础 `FixedOp.op_id`，例如 `eval.rag_answer`。

示例：

```python
from collections.abc import Mapping
from evo.artifact_runtime.kernel import Materializer, MaterializerContext

from .load import load_corpus

def dataset_load_materializer() -> Materializer:
    def materialize(ctx: MaterializerContext, inputs: Mapping[str, object]) -> Mapping[str, object]:
        source_config = inputs.get('source_config')
        if not isinstance(source_config, Mapping):
            raise TypeError('input source_config must be a mapping')
        return {'report': load_corpus(source_config)}

    return materialize
```

禁止事项：

- 不要在 materializer 里读环境变量做兜底。
- 不要在 materializer 里决定流程跳转。
- 不要在 materializer 里直接写 store。
- 不要在 materializer 里知道 graph 或 flow。
- 不要保留旧 `OperationServices`。

需要通用模型生成、判断、总结的 operation 统一使用 `evo/llm.py` 提供的 `LazyLLMClient`：

```python
from evo.llm import LazyLLMClient

def eval_judge_answer_materializer(llm_config=None):
    llm = LazyLLMClient(llm_config=llm_config)

    def materialize(ctx, inputs):
        prompt = build_judge_prompt(inputs['answer'], inputs['policy'])
        return {'judge': judge_answer(llm(prompt, stream=False))}

    return materialize
```

`LazyLLMClient` 是惰性初始化：构造本身不会校验模型配置，第一次调用时才会创建底层 LazyLLM 模型。如果 `llm_config` 里没有对应角色配置，此时直接失败，不做 fallback。

注意当前 `eval.rag_answer` 代表“调用被评测 RAG/Chat 目标服务”，由 `target_config.target_chat_url`、`dataset_id`、`algorithm_id` 和可选 `llm_config` 组成 HTTP 请求，发送到目标 chat endpoint。后续迁移时不要把目标系统调用强行包装成 evo 自己的模型调用；`LazyLLMClient` 只用于 evo 自己需要模型完成的生成/评审/总结类 operation。

## 6. Runtime：执行 next op 并提交产物

实现：`artifact_runtime/kernel/runtime.py`

Runtime 的职责：

- 读取 `store.effective_artifacts(run_id)`。
- 调一次 `graph.next_ops(...)`。
- 读取 input refs 的 payload。
- 调 materializer。
- 调 `store.commit_outputs(...)` 原子提交输出。

一个 tick 基于本次开始时的一次 effective snapshot，顺序执行当时返回的 ready ops，直到全部成功或遇到第一个非 ok。它不会在同一个 tick 内因为前一个 op 新提交了输出就重新计算下游 next ops；下游要等下一次 tick。

它不负责：

- pause/resume/cancel/retry。
- 用户命令幂等。
- 业务动作解析。
- message intent 或 auto agent。
- HTTP API。

构建 runtime 通常不直接手写，而是通过 evo adapter：

```python
from evo.artifact_runtime.kernel import SQLiteArtifactStore
from evo.artifact_runtime.evo.adapter import build_evo_artifact_adapter
from evo.artifact_runtime.evo.flow_ops import default_evo_ops
from evo.operations.dataset.materializers import dataset_materializers
from evo.operations.eval.materializers import eval_materializers
from evo.operations.analysis.materializers import analysis_materializers

store = SQLiteArtifactStore('/path/to/evo-data')
ops = default_evo_ops(('case_0001', 'case_0002'))
materializers = {
    **dataset_materializers(llm_config),
    **eval_materializers(llm_config),
    **analysis_materializers(),
}
adapter = build_evo_artifact_adapter(store, ops, materializers)
```

执行一个 tick：

```python
tick = adapter.tick(run_id)
```

`tick.status` 可能是：

- `ok`：本 tick 选中的 ops 全部成功。
- `idle`：当前没有可执行 op。
- `stale`：执行过程中输入 ref 变了、输出已经全部 effective，或 materialization claim 被其他执行占用。
- `failed`：materializer 或 store 发生失败。
- `conflict`：idempotency key 冲突。

如果前面的 op 已成功、后面的 op 返回 stale/failed/conflict，tick 总状态会是后者；`tick.ops` 仍包含前面已经完成的 ok 结果。

## 7. Evo artifact 层：默认产物和动作

实现：

- `artifact_runtime/evo/catalog.py`
- `artifact_runtime/evo/flow.py`
- `artifact_runtime/evo/flow_ops.py`
- `artifact_runtime/evo/actions.py`
- `artifact_runtime/evo/use_cases.py`
- `artifact_runtime/evo/progress.py`

### 默认步骤和 step root

当前默认五步：

```text
dataset -> eval -> analysis -> repair -> abtest
```

每步 root：

```text
dataset  -> eval.dataset
eval     -> eval.summary
analysis -> analysis.summary
repair   -> repair.verified_patch
abtest   -> abtest.comparison
```

注意当前实现状态：

- `catalog.py` 和 `default_evo_ops(...)` 已经声明 dataset/eval/analysis/repair/abtest 五步图。
- 当前已经迁移并注册 materializer 的主干是 dataset、eval、analysis。
- repair 和 abtest 的图节点已存在，但业务 materializer 尚未迁移完成；如果继续执行到这些节点，runtime 会因为缺少 materializer 返回 failed。
- 因此当前真实可跑范围应按 materializer 注册情况判断，不要把“图已声明”理解成“业务已可运行”。

`EvoFlowSpec` 是 artifact key 计算器：

```python
from evo.artifact_runtime.evo.flow import EvoFlowSpec

spec = EvoFlowSpec(EvoFlowSpec.case_ids(2))
spec.step_output_keys('eval')
spec.read_case_artifact('case_0001', 'eval_answer')
spec.rerun_case_stage('case_0001', 'eval')
spec.jump_to_step('analysis')
```

它不读写 store，不执行 runtime。

### 种子产物

一个 run 要开始执行，至少要写入当前图需要的 external seed：

```python
from evo.artifact_runtime.kernel import ArtifactKey
from evo.artifact_runtime.evo import catalog as C

adapter.commit_external(run_id, ArtifactKey.of(C.RUN_CONFIG), run_config, idempotency_key='seed:run_config')
adapter.commit_external(run_id, ArtifactKey.of(C.CORPUS_SOURCE_CONFIG), source_config, idempotency_key='seed:source')
adapter.commit_external(run_id, ArtifactKey.of(C.EVAL_TARGET_CONFIG), target_config, idempotency_key='seed:target')
adapter.commit_external(run_id, ArtifactKey.of(C.EVAL_POLICY), eval_policy, idempotency_key='seed:eval_policy')
```

repair/abtest 阶段还需要：

```python
adapter.commit_external(run_id, ArtifactKey.of(C.REPAIR_POLICY), repair_policy, idempotency_key='seed:repair_policy')
adapter.commit_external(run_id, ArtifactKey.of(C.ABTEST_CANDIDATE_CONFIG), candidate_config, idempotency_key='seed:candidate')
```

没有对应 seed，graph 就不会返回依赖它的 op。

### Action

Action 是外部 query/mutation 的统一薄入口：

```python
from evo.artifact_runtime.evo.actions import (
    ReadProgressSnapshot,
    RerunCaseStage,
    dispatch_evo_query,
    dispatch_evo_mutation,
)

progress = dispatch_evo_query(adapter, spec, run_id, ReadProgressSnapshot())
result = dispatch_evo_mutation(
    adapter,
    spec,
    run_id,
    RerunCaseStage('case_0001', 'eval', idempotency_key='cmd:rerun:case_0001:eval'),
)
```

Action 只把业务动作翻译成 artifact 读写：

- read step root -> 读当前 effective root record。
- read case artifact -> 读某个 case 的当前 effective record。
- edit artifact -> 基于 ref + JSON Pointer 生成新 external version。
- rerun case stage -> invalidate 该 case 对应阶段产物。
- rerun step -> invalidate 该 step 所有产物。
- jump to step -> invalidate 从该 step 到后续步骤的所有产物。

Action 不执行 tick。修改产物后，需要外部继续调用 flow continue 或 adapter tick。

`EditArtifact` 当前限制：

- `ref` 必须可读。
- JSON Pointer 必须指向已存在路径。
- 不允许替换根对象。
- 不支持数组 `-` append。
- 写入使用 `expected_ref=ref` 做当前性检查；如果该 ref 已不再是 effective ref，store 返回 stale。

## 8. artifact_flow：命令、暂停、放行

实现：

- `artifact_flow/commands.py`
- `artifact_flow/state.py`
- `artifact_flow/gate.py`
- `artifact_flow/service.py`
- `artifact_flow/query.py`

artifact_flow 是当前系统的流程控制层，但它仍然是 artifact-centric：

- 它不保存业务产物。
- 它不决定 op 依赖。
- 它不执行业务函数。
- 它只管理命令幂等、暂停、恢复、取消、失败重试、checkpoint。

### SQLiteFlowGate

`SQLiteFlowGate` 和 artifact store 共用同一个 SQLite 文件：

```python
from evo.artifact_flow.gate import SQLiteFlowGate

gate = SQLiteFlowGate('/path/to/evo-data')
```

它会在同一个 `artifact_store.sqlite3` 内创建：

- `flow_gates`
- `flow_command_receipts`

这保证一个 evo run 不需要多个数据库。

### FlowService

`FlowService` 是外部服务应该调用的入口：

```python
from evo.artifact_flow.gate import SQLiteFlowGate
from evo.artifact_flow.service import FlowService
from evo.artifact_flow.commands import ContinueFlow
from evo.artifact_flow.state import CheckpointPolicy
from evo.artifact_runtime.kernel import SQLiteArtifactStore
from evo.artifact_runtime.evo.adapter import build_evo_artifact_adapter

gate = SQLiteFlowGate('/path/to/evo-data')
service = FlowService(
    gate,
    adapter_factory=lambda: build_evo_artifact_adapter(
        SQLiteArtifactStore('/path/to/evo-data'),
        ops,
        materializers,
    ),
    spec=spec,
    checkpoint_policy=CheckpointPolicy(),
    tick_limit=100,
)

result = service.handle(run_id, ContinueFlow(command_id='cmd:continue:dataset', until_step='dataset'))
```

命令：

- `ContinueFlow(command_id, until_step='')`
- `PauseFlow(command_id)`
- `ResumeFlow(command_id)`
- `CancelFlow(command_id)`
- `RetryFlow(command_id)`
- `ApplyArtifactMutation(command_id, mutation)`

`command_id` 必须稳定且唯一。相同 command_id + 相同请求会 replay；相同 command_id + 不同请求会 conflict。

当前幂等落库边界要按实现理解：

- gate command、正常 recorded continue、正常 recorded artifact mutation 会写 `flow_command_receipts`。
- `tick_limit` 这类直接返回 `_result(...)` 的路径当前不写 receipt。
- `ApplyArtifactMutation` 如果 mutation idempotency key 不匹配，或底层 store idempotency conflict，当前直接返回 failed/conflict，不写 flow receipt。

因此调用方应把 `command_id` 当成必须稳定的命令身份，但不要假设所有异常路径都已经持久化成 receipt。

`ApplyArtifactMutation` 还有一个硬约束：`command.command_id` 必须等于内部 `mutation.idempotency_key`，否则 `FlowService` 直接返回 failed。

### ContinueFlow

`ContinueFlow` 会循环执行 `adapter.tick(run_id)`，直到出现以下情况：

- 到达非最终 step 的 checkpoint，outcome 里是 `status='paused'`，`command_status` 映射为 `blocked`。
- 到达最终 step 且最终 root 完成，返回 `ok`。
- `until_step` 如果不是最终 step，达到该 step root 后也会以 checkpoint 形式暂停，而不是直接返回 `ok`。
- 没有可执行 op，`command_status='ok'`，`command_outcome={'status': 'idle'}`。
- runtime failed/conflict，flow 进入 failed。
- tick 次数超过 `tick_limit`。
- 外部 pause/cancel 改变 gate state。

`until_step` 是步骤名，不是 tick 数：

```python
ContinueFlow('cmd:continue:eval', until_step='eval')
```

默认 checkpoint policy 是：

```python
CheckpointPolicy(pause_after_steps=('dataset', 'eval', 'analysis', 'repair'))
```

也就是说 dataset/eval/analysis/repair 的当前 root ref 如果尚未 released，就会自动暂停，等待用户或上层服务确认，再 `ResumeFlow` 后继续。released checkpoint 是 artifact ref contract：

```text
released_checkpoints[step] == effective_artifacts[root_key(step)]
```

`ResumeFlow` release pending checkpoint 前会校验该 ref 仍是对应 step root 的 effective ref；如果已经 stale，flow 会进入 failed 并写入 `last_error`，不会盲目 release。rerun/edit/invalidate 后如果同一步或下游 step 的 effective root 变化，`ApplyArtifactMutation` 会基于 mutation 后的 `effective_artifacts` 裁剪 stale released checkpoints，而不是按 mutation 类型手写依赖规则。

### Pause / Resume / Cancel / Retry

```python
service.handle(run_id, PauseFlow('cmd:pause:1'))
service.handle(run_id, ResumeFlow('cmd:resume:1'))
service.handle(run_id, CancelFlow('cmd:cancel:1'))
service.handle(run_id, RetryFlow('cmd:retry:1'))
```

语义：

- pause：把 flow gate 置为 paused，正在 continue 的循环会观察到 stale/interrupted。
- resume：只有 paused 可恢复；如果存在 pending checkpoint，必须先通过 checkpoint contract 校验，才会把该 checkpoint 记录为 released。
- cancel：把 flow gate 置为 cancelled；不会删除 artifact。
- retry：只有 failed 可重置为 idle；不自动修改 artifact，也不是全量 rerun。retry 后的 `ContinueFlow` 仍会校验 released checkpoint；如果 dataset checkpoint 仍有效，eval 失败后的 retry 会从 eval 继续。需要从指定步骤重跑时，应使用显式 rerun/invalidate mutation。

这些状态是流程门闩，不是业务 step 状态。产物是否有效仍看 `effective_artifacts`。

`ContinueFlow` 进入 tick 循环前会校验所有 released checkpoint。如果发现 released ref 不再等于 effective root ref，会把 flow gate 写为 failed，`last_error` 包含 stale step、released ref、effective ref 和建议动作；旧数据或非原子写入导致的 checkpoint 不一致不会被静默裁剪。

### FlowQueryService

查询不要走 `FlowService.handle(...)`。当前有单独的薄查询门面：

```python
from evo.artifact_flow.query import FlowQueryService
from evo.artifact_runtime.evo.actions import ReadProgressSnapshot

query = FlowQueryService(gate, adapter_factory, spec)
snapshot = query.snapshot(run_id)
progress = query.progress(run_id)
records = query.read(run_id, ReadProgressSnapshot())
```

`FlowQueryService` 只读：

- `snapshot(run_id)` 返回 flow gate 状态、pending checkpoint、released checkpoints、progress 和 checkpoint projection。
- `progress(run_id)` 根据 `effective_artifacts` 推导每个 step 的 root 和输出完成情况。
- `read(run_id, query)` 调用 `dispatch_evo_query(...)` 读取 step root 或 case artifact。

它不写 store，不执行 tick，不处理命令幂等。

checkpoint projection 是展示/查询字段，不是新的持久化状态：

```json
{
  "current_step": "eval",
  "first_missing_step": "eval",
  "last_released_step": "dataset",
  "checkpoint_state": "valid",
  "retry_from_step": "eval"
}
```

`current_step`、`retry_from_step` 等字段由 `effective_artifacts` 和 `released_checkpoints` 推导，前端/Core 只能展示和透传，不能用它们改写流程边界。

## 9. 后续迁移 operation 的标准步骤

迁移旧 evo operation 时按下面顺序做。

### 1. 迁移业务函数

把旧 operation 的核心业务函数迁到 `evo/operations/<stage>/...py`。

要求：

- 函数输入输出使用普通 Python 数据结构。
- 不依赖旧 `OperationServices`。
- 不直接 import runtime/store/graph/flow。
- 需要 evo 自己调用模型生成、判断、总结时，接收 `llm`，只调用 `llm.llm_complete(...)`。
- 需要调用被评测目标系统或真实外部服务时，把调用放在该 operation 的业务边界内，例如 `eval.rag_answer` 通过 `target_config.target_chat_url` 调用目标 chat endpoint；不要新建一套跨 operation 的 service 层。

### 2. 写薄 materializer

在 `evo/operations/<stage>/materializers.py` 注册：

```python
def xxx_materializers(llm_config=None) -> dict[str, Materializer]:
    return {'stage.op_id': stage_op_materializer(llm_config)}
```

materializer 名称必须等于 `FixedOp.op_id`。

### 3. 在默认 graph 中声明产物关系

修改 `artifact_runtime/evo/flow_ops.py`：

- 新增 `FixedOp` 子类。
- 只声明 artifact input/output。
- partition 规则用 `same_partition`、`unpartitioned_to_all`、`all_to_unpartitioned`。
- 不写执行逻辑。

### 4. 在 catalog 中声明产物名

修改 `artifact_runtime/evo/catalog.py`：

- 新增 artifact id 常量。
- 如果是 step root，更新 `ROOTS`。
- 如果是 step 输出，更新 `OUTPUTS`。
- 如果要支持读取 case，更新 `READ_CASE`。
- 如果要支持 rerun case stage，更新 `RERUN_CASE_STAGE`。

### 5. 接入 materializer map

把新 stage 的 materializers 传给 `build_evo_artifact_adapter(...)`。

如果新增的是 `all_to_unpartitioned()` 汇总类 operation，要通过 runtime/FlowService 验证 tuple 输入形状。当前不保留旧 HTTP materialize 调试桥接，不能用临时 JSON list 转 tuple 的方式替代 runtime 契约。

### 6. 验证

至少验证：

- `DAGGraph.validate()` 通过。
- seed 缺失时不会错误执行 op。
- seed 齐全后 `ContinueFlow(... until_step='xxx')` 能生成对应 step root。
- 产物结构符合旧 evo 功能需求。
- rerun/invalidate 后，下游旧产物退出 effective snapshot。
- 重复 command_id 同请求 replay，不同请求 conflict。
- 不出现旧 `OperationServices`、绕过 `evo/llm.py` 的模型调用、为了迁移临时加入的环境变量兜底。

## 10. 当前 service/api.py 的用法

当前 `evo/service/api.py` 只保留健康检查入口，不承载旧 flow runtime、message intent、auto agent 或 operation materialize 调试桥接。dataset 能力的稳定入口是下面的 runtime/FlowService 组装方式；后续如需 HTTP 层，应直接调用 `FlowService.handle(...)`，不要恢复旧 controller/service 结构。

## 11. 推荐完整组装方式

```python
from evo.artifact_flow.commands import ContinueFlow
from evo.artifact_flow.gate import SQLiteFlowGate
from evo.artifact_flow.service import FlowService
from evo.artifact_runtime.evo import catalog as C
from evo.artifact_runtime.evo.adapter import build_evo_artifact_adapter
from evo.artifact_runtime.evo.flow import EvoFlowSpec
from evo.artifact_runtime.evo.flow_ops import dataset_evo_ops
from evo.artifact_runtime.kernel import ArtifactKey, SQLiteArtifactStore
from evo.operations.dataset import dataset_materializers

root = '/var/lib/lazymind/evo/runs'
run_id = 'run-001'
llm_config = {
    'evo_llm': {
        'source': 'deepseek',
        'type': 'llm',
        'name': 'deepseek-v4-flash',
        'api_key': '...',
    }
}

spec = EvoFlowSpec(EvoFlowSpec.case_ids(2))
ops = dataset_evo_ops(spec.cases)
materializers = dataset_materializers(spec.cases)

def adapter_factory():
    store = SQLiteArtifactStore(root)
    return build_evo_artifact_adapter(store, ops, materializers)

adapter = adapter_factory()
adapter.commit_external(run_id, ArtifactKey.of(C.RUN_CONFIG), {}, idempotency_key='seed:run_config')
adapter.commit_external(run_id, ArtifactKey.of(C.CORPUS_SOURCE_CONFIG), {}, idempotency_key='seed:source_config')
flow = FlowService(
    SQLiteFlowGate(root),
    adapter_factory,
    spec,
)

result = flow.handle(run_id, ContinueFlow('cmd:continue:dataset', until_step='dataset'))
```

实际服务中不要共享同一个 `EvoArtifactAdapter` 给多个线程。当前 adapter 有 owner-thread 检查，store 内部也持有一个 sqlite connection；服务请求里应通过 factory 新建 store 和 adapter，或确保同一个 store/adapter 只在创建它的线程内使用。上面的示例让 `adapter_factory()` 每次新建 `SQLiteArtifactStore` 和 adapter，就是为了避免跨线程复用同一个 SQLite connection。

## 12. 真实测试建议

后续迁移时不要只跑本地脚本。建议按三层验收：

1. 静态契约：
   - `python -m py_compile` 通过。
   - `DAGGraph.validate()` 通过。
   - 搜索确认迁移代码没有旧 `OperationServices`、绕过 `evo/llm.py` 的模型调用、为了跑通而新增的临时环境变量兜底。

2. flow 验收：
   - 当前稳定入口是服务层 `FlowService.handle(...)`；完整 flow HTTP API 还未接入，接入后再补 HTTP 级 flow 验收。
   - 验证 continue 到 dataset。
   - 验证 checkpoint pause/resume。
   - 验证 cancel/retry 不改写 artifact。

如果真实模型、知识库、检索服务不可用，应记录为环境阻塞，不要换 mock 或临时模型声称通过。

## 13. 常见错误

不要这样做：

- 在 operation 里直接写 store。
- 在 materializer 里读大量环境变量兜底。
- 在 graph op 里写执行逻辑。
- 新增 step status 来判断产物有效性。
- 把 pause/cancel/retry 放进 runtime。
- 为单个 operation 新建一套 service/container/registry。
- 为了跑通测试使用和真实服务不同的模型配置。

应该这样做：

- 业务动作先转成 `EvoMutation` 或 `FlowCommand`。
- 产物变化通过 `commit_external` 或 `invalidate` 表达。
- Runtime 只 tick。
- FlowService 只处理命令和 checkpoint。
- Graph 只从 effective snapshot 推导 next ops。
- Store 只保存 artifact 事实。

## 14. 当前尚未完成的集成点

当前代码已经有 kernel、evo adapter/action、artifact_flow、dataset/eval/analysis materializers 的主干，但仍需后续补齐：

- 完整 evo HTTP API 接入 `FlowService`。
- eval / analysis / repair / abtest operation 迁移。
- 真实端到端服务测试固定入口。

做这些工作时仍按本文边界推进，不要把旧 evo 的 controller/service 结构僵硬搬回新 runtime。
