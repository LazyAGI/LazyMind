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
}

export interface Transition {
  to: string;
  condition: string;
}

export interface StepNode {
  id: string;
  label: string;
  mode: 'human' | 'auto';
  inputs: string[];
  outputs: string[];
  transitions: Transition[];
  /** How to follow outgoing transitions. 'all' triggers all matching exits simultaneously (default).
   *  'choice' picks the first matching exit exclusively (conditional routing). */
  route?: 'all' | 'choice';
  /** Natural-language condition under which this step is skipped entirely. */
  skipif?: string;
}

export interface NodeLayout {
  x: number;
  y: number;
  /** Optional persisted width; defaults to NODE_WIDTH when absent. */
  width?: number;
}

export interface GraphModel {
  /** step nodes keyed by id, plus virtual __start__ / __end__ */
  nodes: StepNode[];
  /** slot definitions keyed by id */
  slots: Record<string, SlotDef>;
  /** layout positions per node id */
  layout: Record<string, NodeLayout>;
  /**
   * Conditional transitions out of the virtual __start__ node.
   * Allows multiple possible entry points selected by condition
   * (e.g. "user provided outline" → write_body, else → write_outline).
   * Empty array means no explicit start is configured.
   */
  startTransitions: Transition[];
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
  startTransitions: [],
});
