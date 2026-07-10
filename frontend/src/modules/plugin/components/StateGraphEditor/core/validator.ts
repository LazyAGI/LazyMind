import type { GraphModel } from './model';
import { VIRTUAL_END, VIRTUAL_START } from './model';

export interface ValidationError {
  code: string;
  message: string;
  /** node id if applicable */
  nodeId?: string;
  /** edge source→target if applicable */
  edgeKey?: string;
  /** line number hint for YAML view (optional) */
  line?: number;
}

/**
 * Validate a GraphModel against rules V1–V9 from the plan.
 * Returns an empty array if the model is valid.
 */
export function validateStateGraph(model: GraphModel): ValidationError[] {
  const errors: ValidationError[] = [];
  const { nodes } = model;

  const hasEnd = nodes.some((n) => n.transitions.some((t) => t.to === VIRTUAL_END));

  // Virtual terminal nodes
  // Build adjacency info
  const outgoing = new Map<string, string[]>(); // id → [target ids]
  const incoming = new Map<string, number>(); // id → in-degree count

  for (const node of nodes) {
    outgoing.set(node.id, node.transitions.map((t) => t.to));
    if (!incoming.has(node.id)) incoming.set(node.id, 0);
    for (const t of node.transitions) {
      incoming.set(t.to, (incoming.get(t.to) ?? 0) + 1);
    }
  }

  // V1: must have reachable __end__
  if (!hasEnd) {
    errors.push({
      code: 'V1_MISSING_END',
      message: '状态机必须有至少一个节点转移到 __end__',
    });
  }

  // V1: must have __start__ concept (at least one node with no incoming edges, excluding __start__ node itself)
  const roots = nodes.filter((n) => (incoming.get(n.id) ?? 0) === 0);
  if (roots.length === 0 && nodes.length > 0) {
    errors.push({
      code: 'V1_MISSING_START',
      message: '状态机必须有一个入口节点（无输入边的节点）',
    });
  }

  for (const node of nodes) {
    if (node.id === VIRTUAL_START || node.id === VIRTUAL_END) continue;

    const out = outgoing.get(node.id) ?? [];
    const inDegree = incoming.get(node.id) ?? 0;

    // V2: no isolated nodes (no in AND no out, except __start__)
    if (inDegree === 0 && out.length === 0) {
      errors.push({
        code: 'V2_ISOLATED_NODE',
        message: `节点 "${node.id}" 是孤立节点（无输入也无输出）`,
        nodeId: node.id,
      });
    }

    // V4: every node (except __end__) must have at least one outgoing transition
    if (out.length === 0) {
      errors.push({
        code: 'V4_NO_OUTPUT_EDGE',
        message: `节点 "${node.id}" 没有输出转移边`,
        nodeId: node.id,
      });
    }

    // V5: every node (except __start__ / root) must have at least one incoming edge
    if (inDegree === 0 && roots.length > 1) {
      // only flag if there's more than one root (otherwise it's the single start node)
      errors.push({
        code: 'V5_NO_INPUT_EDGE',
        message: `节点 "${node.id}" 没有输入边`,
        nodeId: node.id,
      });
    }

    const route = node.route ?? 'all';

    // V9 (route:choice): every transition must have a condition
    if (route === 'choice') {
      for (const t of node.transitions) {
        if (!t.condition.trim()) {
          errors.push({
            code: 'V9_EMPTY_CONDITION',
            message: `步骤 "${node.id}" 使用「选择一个」路由时，每条出口必须填写条件`,
            nodeId: node.id,
            edgeKey: `${node.id}->${t.to}`,
          });
        }
      }
    }

    // V10: mixed conditions (some with, some without) cause semantic ambiguity
    if (node.transitions.length > 1) {
      const withCond = node.transitions.filter((t) => t.condition.trim());
      const withoutCond = node.transitions.filter((t) => !t.condition.trim());
      if (withCond.length > 0 && withoutCond.length > 0) {
        errors.push({
          code: 'V10_MIXED_CONDITIONS',
          message: `步骤 "${node.id}" 的出口中，部分有条件、部分无条件，语义不明确。请确保所有出口都有条件（route:choice）或都无条件（route:all 默认触发）`,
          nodeId: node.id,
        });
      }
    }

    // V11 (route:all parallel branches): sub-steps must not have multiple exits
    if (route === 'all' && node.transitions.length > 1) {
      for (const t of node.transitions) {
        const targetNode = nodes.find((n) => n.id === t.to);
        if (targetNode && targetNode.transitions.length > 1) {
          errors.push({
            code: 'V11_PARALLEL_BRANCH_MULTI_EXIT',
            message: `步骤 "${targetNode.id}" 是并行分支子步骤，不允许再有多个出口（禁止二次分叉）`,
            nodeId: targetNode.id,
          });
        }
      }
    }
  }

  // V3: no directed cycles (DFS)
  const cycleNodes = detectCycles(nodes, outgoing);
  for (const id of cycleNodes) {
    errors.push({
      code: 'V3_CYCLE',
      message: `检测到有向环，节点 "${id}" 参与了循环`,
      nodeId: id,
    });
  }

  // V7: required input slots must be produced by topologically prior nodes
  if (cycleNodes.size === 0) {
    const topoOrder = topoSort(nodes, outgoing);
    const produced = new Set<string>();
    for (const nodeId of topoOrder) {
      const node = nodes.find((n) => n.id === nodeId);
      if (!node) continue;
      for (const inp of node.inputs) {
        // Only enforce slot-produced constraint for required inputs.
        if (inp.required && !produced.has(inp.slot)) {
          errors.push({
            code: 'V7_INPUT_NOT_PRODUCED',
            message: `节点 "${nodeId}" 引用的输入 slot "${inp.slot}" 未由前序节点产出`,
            nodeId: nodeId,
          });
        }
      }
      for (const out of node.outputs) {
        produced.add(out.slot);
      }
    }
  }

  // V8: slot references must be defined in plugin.yaml slots
  const slotIds = new Set(Object.keys(model.slots));
  for (const node of nodes) {
    for (const s of [...node.inputs, ...node.outputs]) {
      if (slotIds.size > 0 && !slotIds.has(s.slot) && s.slot !== '') {
        errors.push({
          code: 'V8_UNKNOWN_SLOT',
          message: `节点 "${node.id}" 引用了未在 slots 中定义的 slot "${s.slot}"`,
          nodeId: node.id,
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

function topoSort(nodes: GraphModel['nodes'], outgoing: Map<string, string[]>): string[] {
  const nodeSet = new Set(nodes.map((n) => n.id));
  const incoming = new Map<string, number>();
  for (const node of nodes) {
    if (!incoming.has(node.id)) incoming.set(node.id, 0);
    for (const next of outgoing.get(node.id) ?? []) {
      if (nodeSet.has(next)) {
        incoming.set(next, (incoming.get(next) ?? 0) + 1);
      }
    }
  }

  const queue = nodes.filter((n) => (incoming.get(n.id) ?? 0) === 0).map((n) => n.id);
  const result: string[] = [];

  while (queue.length > 0) {
    const id = queue.shift()!;
    result.push(id);
    for (const next of outgoing.get(id) ?? []) {
      if (!nodeSet.has(next)) continue;
      const deg = (incoming.get(next) ?? 1) - 1;
      incoming.set(next, deg);
      if (deg === 0) queue.push(next);
    }
  }

  return result;
}
