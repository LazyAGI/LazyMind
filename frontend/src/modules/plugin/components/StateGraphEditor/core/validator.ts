import type { GraphModel } from './model';
import { expressionMaterials, VIRTUAL_END, VIRTUAL_START } from './model';

export interface ValidationError {
  code: string;
  message: string;
  severity?: 'error' | 'warning' | string;
  /** node id if applicable */
  nodeId?: string;
  /** edge source→target if applicable */
  edgeKey?: string;
  /** material id if applicable */
  materialId?: string;
  /** structured diagnostic context returned by the authoritative validator */
  details?: Record<string, unknown>;
  /** line number hint for YAML view (optional) */
  line?: number;
}

/** Cheap, non-authoritative editor checks. Go diagnostics own graph semantics. */
export function validateStateGraph(model: GraphModel): ValidationError[] {
  const errors: ValidationError[] = [];
  const { nodes } = model;

  const nodeIds = new Set([VIRTUAL_START, VIRTUAL_END]);
  const outgoing = new Map<string, string[]>(); // id → [target ids]

  for (const node of nodes) {
    if (nodeIds.has(node.id)) {
      errors.push({ code: 'LOCAL_DUPLICATE_NODE', message: `节点标识 "${node.id}" 重复`, nodeId: node.id });
    }
    nodeIds.add(node.id);
    outgoing.set(node.id, node.transitions.map((t) => t.to));
  }

  for (const node of nodes) {
    if (node.id === VIRTUAL_START || node.id === VIRTUAL_END) continue;

    for (const transition of node.transitions) {
      if (!nodeIds.has(transition.to)) {
        errors.push({
          code: 'LOCAL_DANGLING_EDGE',
          message: `边 ${node.id} → ${transition.to} 指向不存在的节点`,
          nodeId: node.id,
          edgeKey: `${node.id}->${transition.to}`,
        });
      }
    }
  }

  // Cycle detection is a cheap local hint; Go returns the authoritative code.
  const cycleNodes = detectCycles(nodes, outgoing);
  for (const id of cycleNodes) {
    errors.push({
      code: 'LOCAL_CYCLE',
      message: `检测到有向环，节点 "${id}" 参与了循环`,
      nodeId: id,
    });
  }

  // Obvious material-reference typos can be shown before the debounced Go call.
  const slotIds = new Set(Object.keys(model.slots));
  for (const node of nodes) {
    const refs = [
      ...node.inputs.flatMap((input) => [input.material, ...(input.alternatives ?? [])]),
      ...node.outputs.map((ref) => ref.material),
      ...expressionMaterials(node.skipIf),
    ];
    for (const material of refs) {
      if (slotIds.size > 0 && !slotIds.has(material) && material !== '') {
        errors.push({
          code: 'LOCAL_UNKNOWN_MATERIAL',
          message: `节点 "${node.id}" 引用了未在 slots 中定义的素材 "${material}"`,
          nodeId: node.id,
          materialId: material,
        });
      }
    }
  }
  return errors;
}

function detectCycles(nodes: GraphModel['nodes'], outgoing: Map<string, string[]>): Set<string> {
  const nodeSet = new Set(nodes.map((n) => n.id));
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const inCycle = new Set<string>();

  function dfs(id: string) {
    if (!nodeSet.has(id)) return;
    if (visited.has(id)) return;
    if (visiting.has(id)) {
      inCycle.add(id);
      return;
    }
    visiting.add(id);
    for (const next of outgoing.get(id) ?? []) {
      dfs(next);
    }
    visiting.delete(id);
    visited.add(id);
  }

  for (const node of nodes) {
    dfs(node.id);
  }

  return inCycle;
}
