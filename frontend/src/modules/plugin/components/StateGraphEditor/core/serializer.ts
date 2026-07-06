import jsYaml from 'js-yaml';
import type { GraphModel } from './model';

/**
 * Serialize a GraphModel back to a canonical YAML string.
 * @param model  The graph model to serialize.
 * @param includeLayout  Whether to include x-layout coordinates (default false for display to user).
 */
export function serializeModel(model: GraphModel, includeLayout = false): string {
  const doc: Record<string, unknown> = {};

  // x-layout block (only when includeLayout is true and non-empty)
  if (includeLayout && Object.keys(model.layout).length > 0) {
    const layoutBlock: Record<string, { x: number; y: number; w?: number }> = {};
    for (const [id, pos] of Object.entries(model.layout)) {
      const entry: { x: number; y: number; w?: number } = {
        x: Math.round(pos.x),
        y: Math.round(pos.y),
      };
      if (pos.width != null) entry.w = Math.round(pos.width);
      layoutBlock[id] = entry;
    }
    doc['x-layout'] = layoutBlock;
  }

  // start_transitions (conditional entry points)
  if (model.startTransitions.length > 0) {
    doc['start_transitions'] = model.startTransitions.map((t) => ({
      to: t.to,
      condition: t.condition,
    }));
  }

  // slots block
  if (Object.keys(model.slots).length > 0) {
    const slotsBlock: Record<string, unknown> = {};
    for (const [id, slot] of Object.entries(model.slots)) {
      const entry: Record<string, unknown> = { type: slot.type };
      if (slot.label) entry.label = slot.label;
      if (slot.cardinality === 'list') {
        entry.cardinality = 'list';
        if (slot.ordered) entry.ordered = true;
        if (slot.allow_manual_add !== undefined) entry.allow_manual_add = slot.allow_manual_add;
      }
      if (slot.summary_max_chars != null) entry.summary_max_chars = slot.summary_max_chars;
      slotsBlock[id] = entry;
    }
    doc.slots = slotsBlock;
  }

  // steps array
  doc.steps = model.nodes.map((node) => {
    const step: Record<string, unknown> = {
      id: node.id,
      label: node.label,
      mode: node.mode,
    };
    if (node.route && node.route !== 'all') step.route = node.route;
    if (node.skipif?.trim()) step.skipif = node.skipif;
    if (node.inputs.length > 0) step.inputs = node.inputs;
    if (node.outputs.length > 0) step.outputs = node.outputs;
    if (node.transitions.length > 0) {
      step.transitions = node.transitions.map((t) => {
        const entry: Record<string, unknown> = { to: t.to };
        if (t.condition.trim()) entry.condition = t.condition;
        return entry;
      });
    }
    return step;
  });

  return jsYaml.dump(doc, {
    indent: 2,
    lineWidth: 120,
    noRefs: true,
    quotingType: '"',
  });
}
