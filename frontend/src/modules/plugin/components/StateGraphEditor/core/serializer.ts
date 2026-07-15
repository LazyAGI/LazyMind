import jsYaml from 'js-yaml';
import type { GraphModel } from './model';

/**
 * Serialize a GraphModel back to a canonical state.yml YAML string.
 *
 * Canonical runtime format:
 *  - inputs are an ordered list; required and alternatives encode readiness semantics
 *  - route `when` values are natural-language hints evaluated by ChatAgent
 *  - prompt, tools, acceptance_criteria fields are included when present
 *  - slots block is NOT output (slot definitions live in plugin.yaml)
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

  // NOTE: slots block is intentionally omitted — slot definitions belong in plugin.yaml.

  // transitions block: __start__ entry transitions + per-step transitions
  const transitionsBlock: Record<string, unknown[]> = {};
  if (model.startTransitions.length > 0) {
    transitionsBlock['__start__'] = model.startTransitions.map((t) => {
      const entry: Record<string, unknown> = { to: t.to };
      if (t.when?.trim()) entry.when = t.when.trim();
      else if (t.condition) entry.condition = t.condition;
      return entry;
    });
  }
  for (const node of model.nodes) {
    if (node.transitions.length > 0) {
      transitionsBlock[node.id] = node.transitions.map((t) => {
        const entry: Record<string, unknown> = { to: t.to };
        if (t.when?.trim()) entry.when = t.when.trim();
        else if (t.condition) entry.condition = t.condition;
        return entry;
      });
    }
  }
  if (Object.keys(transitionsBlock).length > 0) {
    doc['transitions'] = transitionsBlock;
    if (model.startRoute && model.startRoute !== 'all') {
      doc['start_route'] = model.startRoute;
    }
  }

  // steps array — descriptive fields only; transitions live in the top-level transitions block
  doc.steps = model.nodes.map((node) => {
    const step: Record<string, unknown> = {
      id: node.id,
      label: node.label,
      mode: node.mode,
    };
    if (node.route && node.route !== 'all') step.route = node.route;
    if (node.skipIf) step.skip_if = node.skipIf;
    else if (node.legacySkipIf?.trim()) step.skip_if = node.legacySkipIf;
    if (node.prompt?.trim()) step.prompt = node.prompt;
    if (node.tools && node.tools.length > 0) step.tools = node.tools;
    if (node.acceptanceCriteria?.trim()) step.acceptance_criteria = node.acceptanceCriteria;
    if (node.inputs.length > 0) {
      step.inputs = node.inputs.map((input) => ({
        material: input.material,
        required: input.required,
        ...(input.required && input.alternatives?.length
          ? { alternatives: input.alternatives.map((material) => ({ material })) }
          : {}),
      }));
    }
    if (node.outputs.length > 0) {
      step.outputs = node.outputs.map((ref) => ({
        material: ref.material,
        ...(ref.required === false ? { required: false } : {}),
      }));
    }
    // transitions are serialized in the top-level transitions block, not inline here
    return step;
  });

  return jsYaml.dump(doc, {
    indent: 2,
    lineWidth: 120,
    noRefs: true,
    quotingType: '"',
  });
}
