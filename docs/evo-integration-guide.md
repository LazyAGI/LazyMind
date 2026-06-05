# Evo 模块集成指南：注册和删除算法

> 本文档面向 evo 模块的开发者。当 evo 生成了新的算法代码后，需要分别向**离线解析（Parsing）**和**在线对话路由（Chat Router）**两个模块注册，使新算法生效；删除老算法时同样需要在两侧执行对应操作。

---

## 系统架构概览

```
┌───────────────┐    register_algorithm()    ┌──────────────────────┐
│               │ ──────────────────────────►│  DocumentProcessor   │
│               │                            │  (离线解析服务)       │
│  evo 模块     │                            │  lazyllm_algorithm   │
│               │                            │  lazyllm_node_group  │
│               │    POST /inner/algorithm/  │                      │
│               │    register                ├──────────────────────┤
│               │ ──────────────────────────►│  Chat Router         │
└───────────────┘                            │  (在线对话路由)       │
                                             │  router_algorithms   │
                                             └──────────────────────┘
```

两侧注册是**相互独立**的，但应先完成解析侧注册（保证文档处理能力就绪），再完成 Chat Router 侧注册（让流量切过来）。

---

## 一、注册新算法

### 1.1 在离线解析（Parsing）侧注册

**调用方式**：Python SDK（lazyllm 框架内部调用，非 HTTP）

```python
from lazyllm.tools.rag.parsing_service import DocumentProcessor
from lazyllm.tools.rag import Document
from lazyllm.tools.rag.doc_impl import NodeGroupType

# 连接到已运行的 DocumentProcessor 服务
processor = DocumentProcessor(url='http://<processor-host>:<port>')

# 构建 store 和 reader（与 build_document.py 保持一致）
store = ...       # _DocumentStore 实例
reader = ...      # DirectoryReader 实例

# 注册算法
# node_groups 描述新算法的切分策略，key 为 ng 名称，value 为配置字典
processor.register_algorithm(
    name='algo_v2',                # 全局唯一的 algo_id
    store=store,
    reader=reader,
    node_groups={
        'block': {
            'group_type': NodeGroupType.CHUNK,
            'transform': GeneralParser(max_length=2048, split_by='\n'),
            'display_name': 'paragraph slice',
        },
        'line': {
            'group_type': NodeGroupType.CHUNK,
            'transform': LineSplitter,
            'parent': 'block',
            'display_name': 'sentence slice',
        },
    },
    display_name='Algorithm v2',
    description='Improved algorithm with better parsing',
    # policy 控制 ng 签名冲突时的行为：
    #   'none'   - 跳过已注册的 reader（默认，安全）
    #   'update' - 允许更新 reader（签名必须匹配）
    #   'force'  - 强制覆盖所有配置（危险，会重建 ng 数据）
    policy='none',
)
```

**register_algorithm 的内部行为**：

1. 在同一个数据库事务中 upsert `lazyllm_node_group` 表（按 ng 名称去重，基于 signature 判断是否变化）
2. upsert `lazyllm_algorithm` 表，`node_group_ids` 字段记录有序 ng id 列表
3. 将 reader 推送给所有 worker，使其能处理新文档

**注意事项**：

- `name` 即 `algo_id`，在整个系统中必须唯一，建议使用版本化命名（如 `general_algo_v2`）
- ng 的 signature 基于 `(ng_name, transform, parent_signature, ref_signature, group_type)` 计算，配置相同的 ng 会复用同一条记录（跨 algo 共享）
- 当前系统固定 `algo_id='general_algo'`（见 `healthcheck.py`），新 algo 注册后需要确认 healthcheck 是否需要更新

**HTTP API 等价查询（查看注册结果）**：

```bash
# 查看所有已注册算法
GET http://<processor-host>:<port>/algo/list

# 查看某算法的 node group 列表
GET http://<processor-host>:<port>/algo/{algo_id}/groups

# 查看某算法的 node group 详情（name/type/display_name）
GET http://<processor-host>:<port>/algo/{algo_id}/group/info
```

---

### 1.2 在 Chat Router 侧注册

**调用方式**：HTTP API

```bash
POST http://<router-host>:<port>/inner/algorithm/register
Content-Type: application/json

{
  "id": "algo_v2",           // 可选；不传则自动生成 UUID，建议与 parsing 侧 algo_id 保持一致
  "name": "algo_v2",         // 人类可读名称
  "code_path": "/app/algo/v2",  // 新版 chat 算法代码的绝对路径
  "instance_count": 2,       // 启动的子进程数
  "config": {                // 透传给子进程的环境变量（dict 的 key/value 均为字符串）
    "LAZYMIND_ALGO_VERSION": "v2",
    "SOME_FEATURE_FLAG": "true"
  }
}
```

**响应示例**：

```json
{
  "algorithm_id": "algo_v2",
  "ports": [18002, 18003]
}
```

**register 的内部行为**：

1. upsert `router_algorithms` 表，`status='starting'`
2. 为每个子进程分配一个端口（从本节点的端口池中顺序申请）
3. 执行 `python -m lazymind.chat.app --port <port>` 启动子进程，注入 `config` 中的环境变量
4. 轮询每个子进程的 `/health` 端点，全部返回 200 后将 `status` 更新为 `'active'` 再响应
5. 后台 HealthChecker 的 `registry-refresh` 循环会在 5s 内将新实例加入全局注册表，流量即可路由过来

**注意事项**：

- `POST /inner/algorithm/register` 是**同步**的，它会阻塞直到所有子进程都健康才返回
- 如果需要设置 AB 流量分发策略，在注册完成后调用：

```bash
PUT http://<router-host>:<port>/inner/ab/strategy
Content-Type: application/json

{
  "weights": {
    "default": 80,
    "algo_v2": 20
  }
}
```

---

## 二、删除老算法

删除操作需要**先清理解析侧数据，再禁用 Chat Router 侧流量**。

### 2.1 在 Chat Router 侧禁用

**第一步**：先禁用路由（停止向老算法发送新流量）

```bash
DELETE http://<router-host>:<port>/inner/algorithm/{algorithm_id}
```

**响应示例**：

```json
{
  "algorithm_id": "algo_v1",
  "status": "disabled"
}
```

**delete 的内部行为**：

1. `router_algorithms.status` 更新为 `'disabled'`（记录保留，不删除，便于审计）
2. 本节点上所有属于该 algo 的子进程执行 SIGTERM（2s 后 SIGKILL）
3. DB 中该节点的 `router_child_processes` 记录标记为 `stopped`
4. 其他 router 节点上的 HealthChecker 在下次 `registry-refresh`（默认 5s）时，检测到 algo 已 `disabled`，将其所有实例从内存注册表中移除，流量自然停止转发

> **重要**：`DELETE` 只在**当前节点**停止子进程，多节点部署时需要在每个 router 节点上分别调用，或等待各节点的 HealthChecker 自动清理（默认最多 30s 超时）。

---

### 2.2 在离线解析（Parsing）侧清理

**第二步**：删除 algo 的解析注册记录

```python
# 删除 algo 记录（从 lazyllm_algorithm 表删除）
processor.drop_algorithm(name='algo_v1')
```

**⚠️ drop_algorithm 不会自动清理 ng 记录**，需要 evo 视情况处理：

**第三步（可选）**：清理该 algo 独占的 ng 记录

```python
# drop_node_group 内部有保护：若该 ng 还被其他 algo 引用，则抛出 ValueError
# 建议用 try/except 处理共享 ng
for ng_name in ['block_v1', 'line_v1']:  # algo_v1 独占的 ng
    try:
        processor.drop_node_group(ng_name)
    except ValueError as e:
        print(f'ng {ng_name} is shared, skipping: {e}')
```

也可以通过 HTTP API 查看哪些 ng 还被引用：

```bash
# 先查看所有 algo 的 ng 列表，判断哪些 ng 不再被其他 algo 引用
GET http://<processor-host>:<port>/algo/list
GET http://<processor-host>:<port>/algo/{other_algo_id}/groups
```

**第四步（可选）**：清理已关联 KB 中的向量切片

如果某些 KB 曾经绑定了 `algo_v1`，需要通过 DocService 触发向量数据的清理：

```bash
# 通过 DocServer 的 unbind_algo 接口，触发删除专属 ng 的向量切片
# 具体接口参见 DocServer API 文档
# unbind_algo 的行为：
#   - 计算 algo_v1 独占的 ng（不被该 KB 内其他 algo 共享的 ng）
#   - 提交 DOC_DELETE 任务（node_group_ids_to_delete = exclusive_ng_ids）
#   - 异步删除 Milvus/OpenSearch 中的对应切片
```

---

## 三、接口速查表

### 离线解析（DocumentProcessor）HTTP API

| 方法 | 路径 | 作用 |
|------|------|------|
| GET | `/health` | 健康检查 |
| GET | `/algo/list` | 查询所有已注册算法 |
| GET | `/algo/{algo_id}/groups` | 查询某算法的活跃 node group |
| GET | `/algo/{algo_id}/group/info` | 查询 ng 的 name/type/display_name |
| POST | `/ng/{group_name}/lazy_mode` | 设置 ng 的 lazy_mode（null/"embed"/"all"） |
| GET | `/ng/{group_name}/lazy_mode` | 查询 ng 的 lazy_mode |
| GET | `/doc/chunks` | 查询文档切片（algo_id, kb_id, doc_id, group） |
| POST | `/doc/add` | 提交文档解析任务 |
| DELETE | `/doc/delete` | 提交文档删除任务 |
| POST | `/doc/cancel` | 取消等待中的任务 |

### Chat Router HTTP API

| 方法 | 路径 | 作用 |
|------|------|------|
| GET | `/health` | 健康检查（含各 algo 实例数） |
| POST | `/api/chat/stream` | 流式对话（通过 ABRouter 选算法后转发） |
| POST | `/inner/algorithm/register` | 注册新 algo 并启动子进程（全部健康后置 active） |
| DELETE | `/inner/algorithm/{algorithm_id}` | 禁用 algo 并停止子进程（记录保留） |
| GET | `/inner/algorithm` | 列出所有 algo 基本信息（不含实例列表） |
| GET | `/inner/algorithm/{algorithm_id}` | 获取单个 algo 详情（含全局实例列表） |
| POST | `/inner/algorithm/{algorithm_id}/restart` | 重启本节点上该算法的所有子进程 |
| PUT | `/inner/ab/strategy` | 更新 AB 分流策略 |
| GET | `/inner/ab/strategy` | 获取当前 AB 策略 |
| DELETE | `/inner/ab/strategy` | 清除 AB 策略（流量全回 default） |
| GET | `/inner/status` | 完整诊断状态 |

---

## 四、完整操作时序

### 注册新算法的推荐时序

```
evo
 │
 │─── 1. 生成新算法代码到 /app/algo/v2
 │
 │─── 2. processor.register_algorithm(name='algo_v2', ...)
 │         写 lazyllm_algorithm + lazyllm_node_group
 │         等待同步完成
 │
 │─── 3. （可选）验证：GET /algo/algo_v2/groups
 │
 │─── 4. POST /inner/algorithm/register  →  router
 │         启动子进程，等待全部健康后返回
 │
 │─── 5. （可选）POST /inner/ab/strategy
 │         { "weights": {"default": 90, "algo_v2": 10} }
 │         小流量灰度验证
 │
 │─── 6. 验证通过后调整流量：
 │         { "weights": {"default": 0, "algo_v2": 100} }
 │
 ▼
新算法全量生效
```

### 删除老算法的推荐时序

```
evo
 │
 │─── 1. （可选）先将老 algo 流量降为 0：
 │         PUT /inner/ab/strategy
 │         { "weights": {"algo_v2": 100} }
 │
 │─── 2. DELETE /inner/algorithm/algo_v1  →  router
 │         禁用并停止子进程
 │         等待其他节点 HealthChecker 感知（约 5-30s）
 │
 │─── 3. processor.drop_algorithm('algo_v1')
 │         删除解析侧 algo 记录
 │
 │─── 4. （可选）清理独占 ng：
 │         processor.drop_node_group('block_v1')
 │         processor.drop_node_group('line_v1')
 │
 │─── 5. （可选）清理向量数据：
 │         通过 DocServer.unbind_algo(kb_id, 'algo_v1') 触发
 │
 ▼
老算法完全下线
```

---

## 五、常见问题

**Q：两侧注册的顺序有严格要求吗？**

建议先完成解析侧注册（`register_algorithm`），再注册 Chat Router。因为一旦 Chat Router 开始接收请求，chat service 可能需要调用解析结果；反之，若解析侧未就绪就切流量，会导致检索时找不到对应 algo 的 ng 数据。

**Q：`register_algorithm` 支持幂等调用吗？**

支持。`register_algorithm` 是 upsert 语义，相同的 `name` 重复调用会更新 `lazyllm_algorithm` 记录，ng 则基于 signature 去重——签名相同的 ng 不会重复写入，签名不同时行为取决于 `policy` 参数。

**Q：evo 修改了部分代码后，用同名 algo 重新注册会怎样？**

有三种情况，行为完全不同：

| 变更内容 | 默认行为（policy='none'） | 说明 |
|---------|--------------------------|------|
| reader 变了 | **静默忽略新 reader** | 危险：改了但不生效，无明显报错，worker 继续用旧 reader |
| ng transform 参数变了（名字不变） | **raise ValueError，注册失败** | 安全：明确失败，不会静默走偏 |
| 只改 chat 业务逻辑（ng/reader 不变） | 正常 upsert algo 记录 | 无问题 |

针对 reader 变更，必须显式传 `policy='force'`：

```python
processor.register_algorithm(..., policy='force')
```

**Q：ng 的 signature 冲突了怎么处理？**

signature 冲突说明 ng 的配置（transform 参数、parent 层级等）变了，但 ng 名称没变。有两个选择：
1. **重命名（推荐）**：使用版本化的 ng 名称（如 `block_v2`），最安全，不影响已有数据
2. **强制覆盖**：设置 `policy='force'`，会覆盖旧 ng 配置，但已存入向量库的切片是按旧配置切的，与新配置不兼容，需要对所有相关文档重新触发 `DOC_REPARSE`

**Q：Chat Router 注册时 `code_path` 和 parsing 侧的 algo_id 需要一致吗？**

`code_path` 是 Chat Router 用来启动 `lazymind.chat.app` 子进程的路径，与 parsing 侧的 `algo_id` 在概念上是对应的，但不是同一个字段。建议在 `config` 中传入一个环境变量（如 `LAZYMIND_ALGO_ID`）来明确两侧的对应关系。

**Q：删除老算法时，向量切片数据一定需要手动清理吗？**

不强制。如果磁盘/向量库空间充足，可以暂时保留孤立的向量数据；孤立数据不会影响在线检索（检索时按 kb_id + ng_id 过滤）。但建议在合适时机清理，避免向量库膨胀。
