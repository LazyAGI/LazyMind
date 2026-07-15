import jsYaml from 'js-yaml';
import type {
  GraphModel,
  MaterialExpression,
  StepInput,
  StepNode,
  StepOutputRef,
  Transition,
} from './model';
import { VIRTUAL_START, VIRTUAL_END } from './model';

// Raw YAML shape after js-yaml.load
interface RawTransition {
  to?: unknown;
  when?: unknown;
  condition?: unknown;
}

interface RawStep {
  id?: unknown;
  label?: unknown;
  mode?: unknown;
  inputs?: unknown;
  input_expression?: unknown;
  optional_inputs?: unknown;
  outputs?: unknown;
  transitions?: unknown;
  route?: unknown;
  skipif?: unknown;
  skip_if?: unknown;
  prompt?: unknown;
  tools?: unknown;
  acceptance_criteria?: unknown;
}

interface RawYaml {
  'x-layout'?: Record<string, unknown>;
  /** Array format (AI-generated drafts): list of step objects. */
  steps?: unknown[] | Record<string, unknown>;
  /** Legacy flat key (pre-refactor). Superseded by transitions.__start__. */
  start_transitions?: unknown;
  /** Canonical format: keyed transition lists per node id. __start__ holds entry transitions. */
  transitions?: Record<string, unknown>;
  /** Legacy: single default entry node id. */
  initial?: unknown;
  /** Route mode for __start__ transitions: 'choice' picks first match; default is 'all'. */
  start_route?: unknown;
}

const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);
const color = (value: unknown): value is string => typeof value === 'string' && (/^#[0-9a-f]{6}$/i.test(value) || /^rgba?\(/i.test(value));
function parseEdgeVisuals(value: unknown): GraphModel['edgeLayout'] {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  const result: GraphModel['edgeLayout'] = {};
  for (const [id, raw] of Object.entries(value)) {
    if (!id.includes('->') || !raw || typeof raw !== 'object' || Array.isArray(raw)) continue;
    const item=raw as Record<string,unknown>; const visual: GraphModel['edgeLayout'][string]={};
    if (['bezier','straight','smoothstep'].includes(String(item.pathType))) visual.pathType=item.pathType as 'bezier'|'straight'|'smoothstep';
    if(typeof item.showArrow==='boolean')visual.showArrow=item.showArrow;if(typeof item.showLabel==='boolean')visual.showLabel=item.showLabel;
    if(finite(item.arrowSize))visual.arrowSize=Math.min(24,Math.max(4,item.arrowSize));
    if(item.stroke&&typeof item.stroke==='object'&&!Array.isArray(item.stroke)){const s=item.stroke as Record<string,unknown>;visual.stroke={...(color(s.color)?{color:s.color}:{}),...(finite(s.width)?{width:Math.min(12,Math.max(1,s.width))}:{}),...(['solid','dashed','dotted'].includes(String(s.style))?{style:s.style as 'solid'|'dashed'|'dotted'}:{})};}
    result[id]=visual;
  }
  return result;
}
function parseNodeStyle(pos: Record<string,unknown>): Pick<GraphModel['layout'][string],'visible'|'fill'|'border'> {
  const out: Pick<GraphModel['layout'][string],'visible'|'fill'|'border'>={};
  if(pos.visible&&typeof pos.visible==='object'&&!Array.isArray(pos.visible)){const allowed=['stepId','label','outputs','approval','conditionalRoute','parallelRoute','skippable'];out.visible=Object.fromEntries(Object.entries(pos.visible).filter(([k,v])=>allowed.includes(k)&&typeof v==='boolean'));}
  if(pos.border&&typeof pos.border==='object'&&!Array.isArray(pos.border)){const b=pos.border as Record<string,unknown>;const parsed={...(['none','solid','dashed','dotted'].includes(String(b.style))?{style:b.style as 'none'|'solid'|'dashed'|'dotted'}:{}),...(finite(b.width)?{width:Math.min(12,Math.max(0,b.width))}:{}),...(color(b.color)?{color:b.color}:{}),...(finite(b.radius)?{radius:Math.min(100,Math.max(0,b.radius))}:{})};if(Object.keys(parsed).length)out.border=parsed;}
  if(pos.fill&&typeof pos.fill==='object'&&!Array.isArray(pos.fill)){const f=pos.fill as Record<string,unknown>;if(['none','solid','linear-gradient'].includes(String(f.type))){const stops=Array.isArray(f.stops)?f.stops.flatMap((raw)=>{if(!raw||typeof raw!=='object'||Array.isArray(raw))return[];const s=raw as Record<string,unknown>;return color(s.color)&&finite(s.offset)&&finite(s.opacity)?[{color:s.color,offset:Math.min(1,Math.max(0,s.offset)),opacity:Math.min(1,Math.max(0,s.opacity))}]:[];}).sort((a,b)=>a.offset-b.offset):undefined;out.fill={type:f.type as 'none'|'solid'|'linear-gradient',...(color(f.color)?{color:f.color}:{}),...(finite(f.opacity)?{opacity:Math.min(1,Math.max(0,f.opacity))}:{}),...(finite(f.angle)?{angle:((f.angle%360)+360)%360}:{}),...(stops&&stops.length>=2?{stops}:{})};}}
  return out;
}

function parseTransitions(raw: unknown): Transition[] {
  if (!Array.isArray(raw)) return [];
  return raw
    .filter((t): t is RawTransition => t !== null && typeof t === 'object')
    .map((t) => {
      const condition = parseExpression(t.condition);
      const when = typeof t.when === 'string' && t.when.trim()
        ? t.when.trim()
        : typeof t.condition === 'string' && t.condition.trim()
          ? t.condition.trim()
          : undefined;
      return {
        to: String(t.to ?? ''),
        ...(when ? { when } : {}),
        ...(condition ? { condition } : {}),
      };
    });
}

function parseExpression(raw: unknown): MaterialExpression | undefined {
  if (raw !== null && typeof raw === 'object' && !Array.isArray(raw)) {
    const entry = raw as Record<string, unknown>;
    const material = String(entry.material ?? '').trim();
    if (material) return { material };
    for (const key of ['all', 'any'] as const) {
      if (Array.isArray(entry[key])) {
        const children = entry[key]
          .map(parseExpression)
          .filter((child): child is MaterialExpression => Boolean(child));
        return { [key]: children };
      }
    }
  }
  return undefined;
}

function materialID(raw: unknown): string {
  if (typeof raw === 'string') return raw.trim();
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return '';
  const entry = raw as Record<string, unknown>;
  return String(entry.material ?? entry.slot ?? entry.id ?? '').trim();
}

function parseMaterialRefs(raw: unknown): Array<{ material: string }> {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((item): Array<{ material: string }> => {
    const material = materialID(item);
    if (!material) return [];
    return [{ material }];
  });
}

function parseInputs(raw: unknown): StepInput[] {
  if (!Array.isArray(raw)) return [];
  const inputs: StepInput[] = [];
  for (const item of raw) {
    const material = materialID(item);
    if (!material) continue;
    const isRequired = item && typeof item === 'object' && !Array.isArray(item)
      && ((item as Record<string, unknown>).required === true
        || (item as Record<string, unknown>).required === 'true');
    const entry = item as Record<string, unknown>;
    const alternatives = isRequired && Array.isArray(entry.alternatives)
      ? entry.alternatives.map(materialID).filter(Boolean)
      : [];
    inputs.push({
      material,
      required: isRequired,
      ...(alternatives.length ? { alternatives } : {}),
    });
  }
  return inputs;
}

function migrateSplitInputs(inputExpression: MaterialExpression | undefined, optionalRaw: unknown): StepInput[] {
  const parseRequired = (expression: MaterialExpression): StepInput | null => {
    if (expression.material !== undefined) {
      return { material: expression.material, required: true };
    }
    if (expression.any?.length && expression.any.every((child) => child.material !== undefined)) {
      return {
        material: expression.any[0].material!,
        required: true,
        alternatives: expression.any.slice(1).map((child) => child.material!),
      };
    }
    return null;
  };
  const requiredExpressions = inputExpression?.all ?? (inputExpression ? [inputExpression] : []);
  const requiredInputs = requiredExpressions
    .map(parseRequired)
    .filter((input): input is StepInput => input !== null);
  const optionalInputs = parseMaterialRefs(optionalRaw)
    .map((ref): StepInput => ({ material: ref.material, required: false }));
  return [...requiredInputs, ...optionalInputs];
}

function parseOutputs(raw: unknown): StepOutputRef[] {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((item): StepOutputRef[] => {
    const material = materialID(item);
    if (!material) return [];
    const required = !(item && typeof item === 'object' && !Array.isArray(item)
      && ((item as Record<string, unknown>).required === false
        || (item as Record<string, unknown>).required === 'false'));
    return [{ material, required }];
  });
}

function parseStep(raw: RawStep, topLevelTransitions?: Record<string, unknown>): StepNode | null {
  if (!raw.id) return null;
  const stepId = String(raw.id);
  const mode = raw.mode === 'auto' ? 'auto' : 'human';
  const splitInputExpression = parseExpression(raw.input_expression);
  const inputs = Array.isArray(raw.inputs)
    ? parseInputs(raw.inputs)
    : migrateSplitInputs(splitInputExpression, raw.optional_inputs);
  const outputs = parseOutputs(raw.outputs);
  const route: StepNode['route'] = raw.route === 'choice' ? 'choice' : raw.route === 'all' ? 'all' : undefined;
  const rawSkip = raw.skip_if ?? raw.skipif;
  const skipIf = parseExpression(rawSkip);
  const legacySkipIf = typeof rawSkip === 'string' && rawSkip.trim() ? rawSkip : undefined;
  const prompt = raw.prompt !== undefined && raw.prompt !== null && String(raw.prompt).trim()
    ? String(raw.prompt)
    : undefined;
  const tools = Array.isArray(raw.tools) ? raw.tools.map(String) : undefined;
  const acceptanceCriteria = raw.acceptance_criteria !== undefined && raw.acceptance_criteria !== null && String(raw.acceptance_criteria).trim()
    ? String(raw.acceptance_criteria)
    : undefined;

  // Prefer top-level transitions[stepId] over inline step.transitions (canonical format).
  const transitionsRaw = (topLevelTransitions && topLevelTransitions[stepId] !== undefined)
    ? topLevelTransitions[stepId]
    : raw.transitions;

  return {
    id: stepId,
    label: String(raw.label ?? raw.id),
    mode,
    inputs,
    outputs,
    transitions: parseTransitions(transitionsRaw),
    ...(route !== undefined && { route }),
    ...(skipIf ? { skipIf } : {}),
    ...(legacySkipIf ? { legacySkipIf } : {}),
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
  let edgeLayout: GraphModel['edgeLayout'] = {};
  if (raw['x-layout'] && typeof raw['x-layout'] === 'object') {
    const rawLayout = raw['x-layout'];
    const rawEdges = rawLayout.$edges;
    if (rawEdges && typeof rawEdges === 'object' && !Array.isArray(rawEdges)) {
      edgeLayout = parseEdgeVisuals(rawEdges);
    }
    for (const [id, value] of Object.entries(rawLayout)) {
      if (id.startsWith('$') || !value || typeof value !== 'object' || Array.isArray(value)) continue;
      const pos = value as Record<string, unknown>;
      layout[id] = {
        x: finite(pos.x) ? pos.x : 0,
        y: finite(pos.y) ? pos.y : 0,
        ...(finite(pos.w) ? { width: Math.max(90,pos.w) } : {}),
        ...(finite(pos.width) ? { width: Math.max(90,pos.width) } : {}),
        ...(finite(pos.height) ? { height: Math.max(64,pos.height) } : {}),
        ...parseNodeStyle(pos),
      };
    }
  }

  // slots block is intentionally omitted — slot definitions live in plugin.yaml, not state.yml.
  const slots: GraphModel['slots'] = {};

  const nodes: StepNode[] = [];
  const topLevelTransitions = (raw.transitions && typeof raw.transitions === 'object')
    ? raw.transitions as Record<string, unknown>
    : undefined;

  // steps can be either an array (AI-generated drafts) or a dict (hand-authored plugins).
  const stepsIterable: Array<[string | null, unknown]> = Array.isArray(raw.steps)
    ? raw.steps.map((s) => [null, s] as [null, unknown])
    : (raw.steps && typeof raw.steps === 'object')
      ? Object.entries(raw.steps as Record<string, unknown>).map(([k, v]) => [k, v])
      : [];

  for (const [dictKey, step] of stepsIterable) {
    if (step !== null && typeof step === 'object') {
      // When steps is a dict, inject the key as the step id if the step object has no id.
      const rawStep: RawStep = (dictKey !== null && !('id' in (step as object)))
        ? { ...(step as RawStep), id: dictKey }
        : step as RawStep;
      const parsed = parseStep(rawStep, topLevelTransitions);
      // Skip virtual terminal nodes — they are always rendered as built-in terminals
      if (parsed && parsed.id !== VIRTUAL_START && parsed.id !== VIRTUAL_END) {
        nodes.push(parsed);
      }
    }
  }

  const startTransitions: Transition[] = (() => {
    // Priority 1: canonical transitions.__start__
    if (raw.transitions && typeof raw.transitions === 'object') {
      const fromStart = (raw.transitions as Record<string, unknown>)['__start__'];
      if (Array.isArray(fromStart) && fromStart.length > 0) {
        return parseTransitions(fromStart);
      }
    }
    // Priority 2: legacy start_transitions flat key
    if (raw.start_transitions) {
      return parseTransitions(raw.start_transitions);
    }
    // Priority 3: legacy initial field — single entry node, no condition
    if (raw.initial != null) {
      const initialId = String(raw.initial).trim();
      if (initialId) return [{ to: initialId }];
    }
    return [];
  })();

  const startRoute: GraphModel['startRoute'] = raw.start_route === 'choice' ? 'choice' : raw.start_route === 'all' ? 'all' : undefined;

  return { nodes, slots, layout, edgeLayout, startTransitions, startRoute };
}
