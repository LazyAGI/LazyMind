# 代码示例：有序 Slot 调序并发安全实现

> 对应 plan.md §2.4 `plugin_slot_order` 表设计与 §3.2 调序逻辑。

---

## 1. DDL

```sql
-- plugin_slot_order：slot 级别的展示顺序，一行一个 slot
CREATE TABLE plugin_slot_order (
  session_id     VARCHAR     NOT NULL,
  slot_id        VARCHAR     NOT NULL,
  order_list     JSONB       NOT NULL,   -- [list_index, ...] 按展示顺序排列，仅含可见项
  order_version  INT         NOT NULL DEFAULT 0,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (session_id, slot_id)
);

-- sub_agent_artifacts：不再存 sort_order，顺序由 plugin_slot_order 管理
ALTER TABLE sub_agent_artifacts
  ADD COLUMN hidden  BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN caption TEXT;

CREATE INDEX idx_saa_slot_visible
  ON sub_agent_artifacts(session_id, slot_id, hidden, list_index);
```

---

## 2. sort_order 查询（动态展开）

```sql
-- 查询某 slot 的可见项，附带 sort_order
WITH ordered AS (
  SELECT
    value::int      AS list_index,
    ordinality::int AS sort_order
  FROM plugin_slot_order,
       jsonb_array_elements(order_list) WITH ORDINALITY
  WHERE session_id = $1 AND slot_id = $2
)
SELECT
  a.*,
  COALESCE(o.sort_order, a.list_index + 1) AS sort_order
FROM sub_agent_artifacts a
LEFT JOIN ordered o USING (list_index)
WHERE a.session_id = $1
  AND a.slot_id    = $2
  AND a.hidden     = FALSE
ORDER BY sort_order, a.list_index;
```

> `COALESCE` 兜底：新增项在 `order_list` 写入前短暂不在映射表中，用 `list_index + 1` 自然兜底，避免返回 NULL。

---

## 3. Go：调序 Handler（乐观锁）

```go
type ReorderRequest struct {
    // 前端传新的 sort_order 排列（当前可见项的 sort_order 值，按期望顺序排列）
    Order   []int `json:"order"   binding:"required"`
    Version int   `json:"version" binding:"required"`
}

func HandleReorder(c *gin.Context) {
    sessionID := c.Param("session_id")
    slotID    := c.Param("slot_id")

    var req ReorderRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    err := db.Transaction(func(tx *gorm.DB) error {
        // 1. 读当前状态，行锁防并发
        var cur SlotOrder
        if err := tx.Raw(
            `SELECT * FROM plugin_slot_order
             WHERE session_id=? AND slot_id=? FOR UPDATE`,
            sessionID, slotID,
        ).Scan(&cur).Error; err != nil {
            return err
        }

        // 2. 乐观锁：version 不匹配说明已被其他请求修改
        if cur.OrderVersion != req.Version {
            return ErrVersionConflict // → 409
        }

        // 3. 将前端的 sort_order 序列翻译回 list_index 序列
        //    cur.OrderList 当前是 [list_index...] 有序数组，ordinality 即 sort_order
        newOrderList, err := translateSortOrderToListIndex(req.Order, cur.OrderList)
        if err != nil {
            return err // sort_order 值超出范围 → 400
        }

        // 4. 校验所有 list_index 均在可见集合内（防 DELETE 并发）
        if err := validateVisible(tx, sessionID, slotID, newOrderList); err != nil {
            return err // → 400
        }

        // 5. 原子写：只写一行
        return tx.Exec(
            `UPDATE plugin_slot_order
             SET order_list=?, order_version=order_version+1, updated_at=now()
             WHERE session_id=? AND slot_id=?`,
            pq.Array(newOrderList), sessionID, slotID,
        ).Error
    })

    switch {
    case errors.Is(err, ErrVersionConflict):
        c.JSON(409, gin.H{"error": "order version conflict, please refresh and retry"})
    case err != nil:
        c.JSON(400, gin.H{"error": err.Error()})
    default:
        c.JSON(200, gin.H{"ok": true})
    }
}

// translateSortOrderToListIndex：cur.OrderList[sort_order-1] = list_index
func translateSortOrderToListIndex(sortOrders []int, curOrderList []int) ([]int, error) {
    result := make([]int, 0, len(sortOrders))
    for _, so := range sortOrders {
        if so < 1 || so > len(curOrderList) {
            return nil, fmt.Errorf("sort_order %d out of range", so)
        }
        result = append(result, curOrderList[so-1])
    }
    return result, nil
}

// validateVisible：确保所有 list_index 当前 hidden=FALSE
func validateVisible(tx *gorm.DB, sessionID, slotID string, listIndexes []int) error {
    var count int64
    tx.Model(&Artifact{}).
        Where("session_id=? AND slot_id=? AND list_index IN ? AND hidden=FALSE",
            sessionID, slotID, listIndexes).
        Count(&count)
    if int(count) != len(listIndexes) {
        return fmt.Errorf("some items are deleted or not found")
    }
    return nil
}
```

---

## 4. Go：逻辑删除时同步更新 order_list

```go
func HandleDeleteItem(c *gin.Context) {
    sessionID := c.Param("session_id")
    slotID    := c.Param("slot_id")
    sortOrder := c.Param("sort_order") // 前端用 sort_order 定位

    err := db.Transaction(func(tx *gorm.DB) error {
        // 1. 读 order_list，找到对应 list_index
        var cur SlotOrder
        tx.Raw(`SELECT * FROM plugin_slot_order
                WHERE session_id=? AND slot_id=? FOR UPDATE`,
               sessionID, slotID).Scan(&cur)

        so, _ := strconv.Atoi(sortOrder)
        if so < 1 || so > len(cur.OrderList) {
            return fmt.Errorf("sort_order out of range")
        }
        listIndex := cur.OrderList[so-1]

        // 2. 逻辑删除
        tx.Exec(`UPDATE sub_agent_artifacts SET hidden=TRUE
                 WHERE session_id=? AND slot_id=? AND list_index=?`,
                sessionID, slotID, listIndex)

        // 3. 从 order_list 移除该 list_index，version +1
        newOrderList := removeFromSlice(cur.OrderList, listIndex)
        tx.Exec(`UPDATE plugin_slot_order
                 SET order_list=?, order_version=order_version+1, updated_at=now()
                 WHERE session_id=? AND slot_id=?`,
                pq.Array(newOrderList), sessionID, slotID)

        // 4. plugin_slot_revisions 对应 list_index → selected=FALSE
        tx.Exec(`UPDATE plugin_slot_revisions SET selected=FALSE
                 WHERE session_id=? AND slot_id=? AND list_index=?`,
                sessionID, slotID, listIndex)

        return nil
    })
    // ... SSE: slot_item_deleted
}
```

---

## 5. 前端：debounce + 409 回滚

```typescript
// useSlotReorder.ts
import { useDebouncedCallback } from 'use-debounce';
import { useRef } from 'react';

export function useSlotReorder(sessionId: string, slotId: string) {
  // 从 GET /slots 响应中缓存 order_version
  const versionRef = useRef<number>(0);

  const doReorder = async (newSortOrder: number[]) => {
    try {
      await api.patch(
        `/plugin-sessions/${sessionId}/slots/${slotId}/order`,
        { order: newSortOrder, version: versionRef.current }
      );
      versionRef.current += 1;
    } catch (err) {
      if (err.response?.status === 409) {
        // 版本冲突：重新拉取最新顺序，UI 回滚
        const latest = await api.get(`/plugin-sessions/${sessionId}/slots`);
        versionRef.current = latest.data.slots[slotId].order_version;
        // 触发父组件用最新 order 重渲染拖拽列表
        onConflict(latest.data.slots[slotId].items);
      }
    }
  };

  // 拖拽完成后 debounce 500ms，消除连续快速拖拽
  const debouncedReorder = useDebouncedCallback(doReorder, 500);

  return { debouncedReorder, versionRef };
}
```

---

## 6. 并发场景一览

| 场景 | 结果 |
| --- | --- |
| 同一用户连续拖拽（debounce 生效） | 只发最后一次请求，无冲突 |
| 同一用户连续拖拽（请求乱序到达） | 旧请求 version 落后 → 409，UI 回滚到最新 |
| 多标签页并发调序（相同 version） | `FOR UPDATE` 串行化，第二个 409，前端各自回滚 |
| DELETE 与 PATCH /order 并发 | DELETE 先：order_list 已移除，PATCH 校验时 validateVisible 失败 → 400；PATCH 先：DELETE 在 order_list 中移除，两者各自原子完成 |

---

## 7. composite_layout 解析

### 7.1 类型定义

```typescript
// composite_layout 节点类型
type LayoutNode =
  | string                          // 单个 slot_id
  | LayoutNode[]                    // 并排（side-by-side）
  | { tabs: LayoutNode[] }          // Tab 切换
  | { slot: LayoutNode; weight?: number }; // 带权重的并排节点

// plugin.yaml 解析后的 composite tab
interface CompositeTab {
  id: string;
  layout: 'composite';
  slots: SlotDef[];
  composite_layout?: LayoutNode[];   // 省略时 = 所有 slot 并排
}
```

### 7.2 解析为渲染树

```typescript
// 将 composite_layout 节点递归解析为渲染用的布局树
type RenderNode =
  | { kind: 'slot';     slotId: string; weight: number }
  | { kind: 'row';      children: RenderNode[] }
  | { kind: 'tabs';     children: RenderNode[] };

function parseLayoutNode(node: LayoutNode, defaultWeight = 1): RenderNode {
  // 字符串：单 slot
  if (typeof node === 'string') {
    return { kind: 'slot', slotId: node, weight: defaultWeight };
  }

  // 数组：并排
  if (Array.isArray(node)) {
    return {
      kind: 'row',
      children: node.map(child => parseLayoutNode(child)),
    };
  }

  // { tabs: [...] }：Tab 切换
  if ('tabs' in node) {
    return {
      kind: 'tabs',
      children: node.tabs.map(child => parseLayoutNode(child)),
    };
  }

  // { slot: ..., weight: N }：带权重的节点
  if ('slot' in node) {
    const inner = parseLayoutNode(node.slot);
    return { ...inner, weight: node.weight ?? 1 };
  }

  throw new Error(`Unknown layout node: ${JSON.stringify(node)}`);
}

// 入口：整个 composite_layout 数组 → 顶层并排 RenderNode
function parseCompositeLayout(
  compositeDef: LayoutNode[] | undefined,
  slotIds: string[]
): RenderNode {
  const layout = compositeDef ?? slotIds; // 省略时退化为全并排
  return parseLayoutNode(layout);
}
```

### 7.3 React 渲染组件

```tsx
// CompositeRow.tsx
interface CompositeRowProps {
  node: RenderNode;
  sortOrder: number;        // 当前行的 sort_order
  slotData: Record<string, SlotItem | undefined>;
}

export function CompositeRow({ node, sortOrder, slotData }: CompositeRowProps) {
  if (node.kind === 'slot') {
    const item = slotData[node.slotId];
    return (
      <div style={{ flex: node.weight }}>
        <SlotCell slotId={node.slotId} item={item} sortOrder={sortOrder} />
      </div>
    );
  }

  if (node.kind === 'row') {
    return (
      <div style={{ display: 'flex', gap: 8 }}>
        {node.children.map((child, i) => (
          <CompositeRow key={i} node={child} sortOrder={sortOrder} slotData={slotData} />
        ))}
      </div>
    );
  }

  if (node.kind === 'tabs') {
    const [activeIdx, setActiveIdx] = useState(0);
    const active = node.children[activeIdx];
    return (
      <div style={{ flex: 1 }}>
        <div className="tab-bar">
          {node.children.map((child, i) => {
            const slotId = getLeafSlotId(child); // 取第一个叶子 slot_id 作为 tab key
            return (
              <button
                key={i}
                className={activeIdx === i ? 'active' : ''}
                onClick={() => setActiveIdx(i)}
              >
                {slotId} {/* 实际用 slot label（含 i18n） */}
              </button>
            );
          })}
        </div>
        <CompositeRow node={active} sortOrder={sortOrder} slotData={slotData} />
      </div>
    );
  }
}

// 取布局节点的第一个叶子 slotId（用于 Tab 标题）
function getLeafSlotId(node: RenderNode): string {
  if (node.kind === 'slot') return node.slotId;
  return getLeafSlotId(node.children[0]);
}
```

### 7.4 完整示例（PPT 场景）

```yaml
# plugin.yaml
composite_layout:
  - [slide_desc, {tabs: [slide_html, slide_notes]}]
```

解析结果（伪 JSON）：

```json
{
  "kind": "row",
  "children": [
    { "kind": "slot", "slotId": "slide_desc", "weight": 1 },
    {
      "kind": "tabs",
      "weight": 1,
      "children": [
        { "kind": "slot", "slotId": "slide_html",  "weight": 1 },
        { "kind": "slot", "slotId": "slide_notes", "weight": 1 }
      ]
    }
  ]
}
```

渲染效果（sort_order=1 行）：

```
┌───────────────┬──────────────────────────────┐
│  slide_desc   │  [HTML预览 ●] [讲稿]          │
│               │──────────────────────────────│
│  第1页描述    │  <div class="slide">...</div> │
└───────────────┴──────────────────────────────┘
```

