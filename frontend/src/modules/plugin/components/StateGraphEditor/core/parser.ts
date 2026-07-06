import jsYaml from 'js-yaml';
import type { GraphModel, SlotDef, StepNode, Transition } from './model';
import { VIRTUAL_START, VIRTUAL_END } from './model';

// Raw YAML shape after js-yaml.load
interface RawTransition {
  to?: unknown;
  condition?: unknown;
}

interface RawStep {
  id?: unknown;
  label?: unknown;
  mode?: unknown;
  inputs?: unknown;
  outputs?: unknown;
  transitions?: unknown;
  route?: unknown;
  skipif?: unknown;
}

interface RawYaml {
  'x-layout'?: Record<string, { x?: number; y?: number; w?: number }>;
  slots?: Record<string, { type?: unknown; label?: unknown; cardinality?: unknown; ordered?: unknown; allow_manual_add?: unknown; summary_max_chars?: unknown }>;
  steps?: unknown[];
  start_transitions?: unknown;
}

function parseTransitions(raw: unknown): Transition[] {
  if (!Array.isArray(raw)) return [];
  return raw
    .filter((t): t is RawTransition => t !== null && typeof t === 'object')
    .map((t) => ({
      to: String(t.to ?? ''),
      condition: String(t.condition ?? ''),
    }));
}

function parseStep(raw: RawStep): StepNode | null {
  if (!raw.id) return null;
  const mode = raw.mode === 'auto' ? 'auto' : 'human';
  const inputs = Array.isArray(raw.inputs) ? raw.inputs.map(String) : [];
  const outputs = Array.isArray(raw.outputs) ? raw.outputs.map(String) : [];
  const route: StepNode['route'] = raw.route === 'choice' ? 'choice' : raw.route === 'all' ? 'all' : undefined;
  const skipif = raw.skipif !== undefined && raw.skipif !== null && String(raw.skipif).trim()
    ? String(raw.skipif)
    : undefined;
  return {
    id: String(raw.id),
    label: String(raw.label ?? raw.id),
    mode,
    inputs,
    outputs,
    transitions: parseTransitions(raw.transitions),
    ...(route !== undefined && { route }),
    ...(skipif !== undefined && { skipif }),
  };
}

function parseSlots(raw: unknown): Record<string, SlotDef> {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {};
  const result: Record<string, SlotDef> = {};
  for (const [id, val] of Object.entries(raw as Record<string, unknown>)) {
    const entry = val && typeof val === 'object' && !Array.isArray(val) ? (val as Record<string, unknown>) : {};
    const slot: SlotDef = {
      id,
      type: String(entry.type ?? 'text'),
      label: entry.label !== undefined ? String(entry.label) : undefined,
    };
    if (entry.cardinality === 'list') slot.cardinality = 'list';
    if (slot.cardinality === 'list') {
      if (entry.ordered === true || entry.ordered === 'true') slot.ordered = true;
      if (entry.allow_manual_add === false || entry.allow_manual_add === 'false') {
        slot.allow_manual_add = false;
      } else if (entry.allow_manual_add === true || entry.allow_manual_add === 'true') {
        slot.allow_manual_add = true;
      }
    }
    if (typeof entry.summary_max_chars === 'number' && entry.summary_max_chars > 0) {
      slot.summary_max_chars = entry.summary_max_chars;
    }
    result[id] = slot;
  }
  return result;
}

/**
 * Parse a YAML string into a GraphModel.
 * Returns null if the YAML has a syntax error.
 * On structural errors, returns the best-effort model.
 */
export function parseYaml(yamlText: string): GraphModel | null {
  let raw: RawYaml;
  try {
    raw = (jsYaml.load(yamlText) ?? {}) as RawYaml;
  } catch {
    return null;
  }

  const layout: GraphModel['layout'] = {};
  if (raw['x-layout'] && typeof raw['x-layout'] === 'object') {
    for (const [id, pos] of Object.entries(raw['x-layout'])) {
      layout[id] = {
        x: typeof pos.x === 'number' ? pos.x : 0,
        y: typeof pos.y === 'number' ? pos.y : 0,
        ...(typeof pos.w === 'number' ? { width: pos.w } : {}),
      };
    }
  }

  const slots = parseSlots(raw.slots);

  const nodes: StepNode[] = [];
  if (Array.isArray(raw.steps)) {
    for (const step of raw.steps) {
      if (step !== null && typeof step === 'object') {
        const parsed = parseStep(step as RawStep);
        // Skip virtual terminal nodes — they are always rendered as built-in terminals
        if (parsed && parsed.id !== VIRTUAL_START && parsed.id !== VIRTUAL_END) {
          nodes.push(parsed);
        }
      }
    }
  }

  const startTransitions = parseTransitions(raw.start_transitions);

  return { nodes, slots, layout, startTransitions };
}
