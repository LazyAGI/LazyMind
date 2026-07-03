// GraphModel is the in-memory representation of a state machine parsed from YAML.
// It is the single source of truth while editing; the YAML text is derived from it.

export interface SlotDef {
  id: string;
  type: string;
  label?: string;
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
}

export interface NodeLayout {
  x: number;
  y: number;
}

export interface GraphModel {
  /** step nodes keyed by id, plus virtual __start__ / __end__ */
  nodes: StepNode[];
  /** slot definitions keyed by id */
  slots: Record<string, SlotDef>;
  /** layout positions per node id */
  layout: Record<string, NodeLayout>;
}

export const VIRTUAL_START = '__start__';
export const VIRTUAL_END = '__end__';

export const createEmptyModel = (): GraphModel => ({
  nodes: [],
  slots: {},
  layout: {},
});
