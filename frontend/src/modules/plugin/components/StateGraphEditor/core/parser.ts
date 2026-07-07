import jsYaml from 'js-yaml';
import type { GraphModel, StepInputRef, StepNode, Transition } from './model';
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
  prompt?: unknown;
  tools?: unknown;
  acceptance_criteria?: unknown;
}

interface RawYaml {
  'x-layout'?: Record<string, { x?: number; y?: number; w?: number }>;
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

/**
 * Parse a single input/output entry, which can be:
 *   - A plain string (legacy format) → converted to { slot: string, required: false }
 *   - An object with { slot, required } (new format)
 */
function parseInputRef(raw: unknown): StepInputRef | null {
  if (typeof raw === 'string') {
    return { slot: raw, required: false };
  }
  if (raw !== null && typeof raw === 'object' && !Array.isArray(raw)) {
    const entry = raw as Record<string, unknown>;
    const slot = String(entry.slot ?? '').trim();
    if (!slot) return null;
    return {
      slot,
      required: entry.required === true || entry.required === 'true',
    };
  }
  return null;
}

function parseInputRefs(raw: unknown): StepInputRef[] {
  if (!Array.isArray(raw)) return [];
  return raw.map(parseInputRef).filter((r): r is StepInputRef => r !== null);
}

function parseStep(raw: RawStep): StepNode | null {
  if (!raw.id) return null;
  const mode = raw.mode === 'auto' ? 'auto' : 'human';
  const inputs = parseInputRefs(raw.inputs);
  const outputs = parseInputRefs(raw.outputs);
  const route: StepNode['route'] = raw.route === 'choice' ? 'choice' : raw.route === 'all' ? 'all' : undefined;
  const skipif = raw.skipif !== undefined && raw.skipif !== null && String(raw.skipif).trim()
    ? String(raw.skipif)
    : undefined;
  const prompt = raw.prompt !== undefined && raw.prompt !== null && String(raw.prompt).trim()
    ? String(raw.prompt)
    : undefined;
  const tools = Array.isArray(raw.tools) ? raw.tools.map(String) : undefined;
  const acceptanceCriteria = raw.acceptance_criteria !== undefined && raw.acceptance_criteria !== null && String(raw.acceptance_criteria).trim()
    ? String(raw.acceptance_criteria)
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
    ...(prompt !== undefined && { prompt }),
    ...(tools !== undefined && { tools }),
    ...(acceptanceCriteria !== undefined && { acceptanceCriteria }),
  };
}

/**
 * Parse a state.yml YAML string into a GraphModel.
 * Returns null if the YAML has a syntax error.
 * On structural errors, returns the best-effort model.
 * Note: slots are NOT parsed here — they live in plugin.yaml and are loaded separately.
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

  // slots block is intentionally omitted — slot definitions live in plugin.yaml, not state.yml.
  const slots: GraphModel['slots'] = {};

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
