// GraphModel is the in-memory representation of a state machine parsed from YAML.
// It is the single source of truth while editing; the YAML text is derived from it.

export interface SlotDef {
  id: string;
  type: string;
  label?: string;
  /** 'single' (default) or 'list' — whether the slot holds one item or a list. */
  cardinality?: 'single' | 'list';
  /** Whether list items are ordered. Only meaningful when cardinality is 'list'. */
  ordered?: boolean;
  /**
   * Whether the user is allowed to manually add items to this list at runtime.
   * Only meaningful when cardinality is 'list'.
   * Default: true if any step uses this slot as input; false otherwise.
   */
  allow_manual_add?: boolean;
  /** Max characters to include when this slot value is injected into a prompt as a summary. */
  summary_max_chars?: number;
  /** Material is supplied by the user/session rather than produced by a step. */
  external?: boolean;
}

export interface MaterialExpression {
  material?: string;
  all?: MaterialExpression[];
  any?: MaterialExpression[];
}

export interface Transition {
  to: string;
  /** Natural-language routing hint evaluated by ChatAgent. */
  when?: string;
  /** Deprecated material route expression, preserved only for migration diagnostics. */
  condition?: MaterialExpression;
}

/** Canonical authoring format for an ordered step input row. */
export interface StepInput {
  material: string;
  required: boolean;
  /** Ordered fallback materials; only valid for required inputs. */
  alternatives?: string[];
}

export interface StepOutputRef {
  material: string;
  /** Legacy/internal conditional output. The visual editor does not expose this setting. */
  required?: boolean;
}

export interface StepNode {
  id: string;
  label: string;
  mode: 'human' | 'auto';
  /** Ordered input rows; the compiler normalizes these into required expressions and optional bindings. */
  inputs: StepInput[];
  /** References to output slots. */
  outputs: StepOutputRef[];
  transitions: Transition[];
  /** How to follow outgoing transitions. 'all' triggers all matching exits simultaneously (default).
   *  'choice' picks the first matching exit exclusively (conditional routing). */
  route?: 'all' | 'choice';
  /** Executable material condition under which this step is bypassed. */
  skipIf?: MaterialExpression;
  /** Preserved only while migrating an invalid natural-language skip condition. */
  legacySkipIf?: string;
  /** Agent prompt for this step; may contain {{slot_id}} references. */
  prompt?: string;
  /** Tool function names available to the agent for this step. */
  tools?: string[];
  /** Natural-language quality criteria the agent must satisfy before completing this step. */
  acceptanceCriteria?: string;
}

export interface NodeLayout {
  x: number;
  y: number;
  /** Optional persisted width; defaults to NODE_WIDTH when absent. */
  width?: number;
  /** Optional fixed height. Missing means content-driven auto height. */
  height?: number;
  visible?: NodeVisibility;
  fill?: NodeFill;
  border?: NodeBorder;
}

export interface NodeVisibility {
  stepId?: boolean;
  label?: boolean;
  outputs?: boolean;
  approval?: boolean;
  conditionalRoute?: boolean;
  parallelRoute?: boolean;
  skippable?: boolean;
}

export interface GradientStop { offset: number; color: string; opacity: number }
export interface NodeFill {
  type: 'none' | 'solid' | 'linear-gradient';
  color?: string;
  opacity?: number;
  angle?: number;
  stops?: GradientStop[];
}
export interface NodeBorder {
  style?: 'none' | 'solid' | 'dashed' | 'dotted';
  width?: number;
  color?: string;
  radius?: number;
}
export interface EdgeVisual {
  stroke?: { color?: string; width?: number; style?: 'solid' | 'dashed' | 'dotted' };
  pathType?: 'bezier' | 'straight' | 'smoothstep';
  showArrow?: boolean;
  arrowSize?: number;
  showLabel?: boolean;
}

export interface GraphModel {
  /** step nodes keyed by id, plus virtual __start__ / __end__ */
  nodes: StepNode[];
  /** slot definitions keyed by id */
  slots: Record<string, SlotDef>;
  /** layout positions per node id */
  layout: Record<string, NodeLayout>;
  /** Presentation-only edge styles, keyed by `source->target`. */
  edgeLayout: Record<string, EdgeVisual>;
  /**
   * Conditional transitions out of the virtual __start__ node.
   * Allows multiple possible entry points selected by condition
   * (e.g. "user provided outline" → write_body, else → write_outline).
   * Empty array means no explicit start is configured.
   */
  startTransitions: Transition[];
  /**
   * How __start__ follows its outgoing transitions.
   * 'all' triggers all simultaneously (default); 'choice' picks the first match.
   */
  startRoute?: 'all' | 'choice';
}

export const VIRTUAL_START = '__start__';
export const VIRTUAL_END = '__end__';

/**
 * Hidden-id prefix. When a user clears the step ID field, we assign a hidden
 * placeholder id so the node remains valid in the model. The canvas never
 * displays ids that start with this prefix.
 */
export const HID_PREFIX = '.hid-';

/** Returns true when the id is a hidden placeholder (not user-assigned). */
export const isHiddenId = (id: string) => id.startsWith(HID_PREFIX);

/** Generate a new hidden placeholder id. */
export const newHiddenId = () => `${HID_PREFIX}${Math.random().toString(36).slice(2, 8)}`;

export const createEmptyModel = (): GraphModel => ({
  nodes: [],
  slots: {},
  layout: {},
  edgeLayout: {},
  startTransitions: [],
  startRoute: undefined,
});

export function expressionMaterials(expr?: MaterialExpression): string[] {
  if (!expr) return [];
  const out: string[] = [];
  const seen = new Set<string>();
  const visit = (current: MaterialExpression) => {
    if (current.material && !seen.has(current.material)) {
      seen.add(current.material);
      out.push(current.material);
    }
    current.all?.forEach(visit);
    current.any?.forEach(visit);
  };
  visit(expr);
  return out;
}

export function removeMaterialFromExpression(
  expr: MaterialExpression | undefined,
  material: string,
): MaterialExpression | undefined {
  if (!expr) return undefined;
  if (expr.material) return expr.material === material ? undefined : expr;
  const key = expr.all ? 'all' : expr.any ? 'any' : undefined;
  if (!key) return expr;
  const children = (expr[key] ?? [])
    .map((child) => removeMaterialFromExpression(child, material))
    .filter((child): child is MaterialExpression => Boolean(child));
  if (children.length === 0) return undefined;
  if (children.length === 1) return children[0];
  return { ...expr, [key]: children };
}

export function formatExpression(expr?: MaterialExpression): string {
  if (!expr) return '';
  if (expr.material) return expr.material;
  const children = expr.all ?? expr.any ?? [];
  const operator = expr.all ? ' AND ' : ' OR ';
  return children.length > 0 ? `(${children.map(formatExpression).join(operator)})` : '';
}
