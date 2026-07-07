// Data model for plugin.yaml — the plugin metadata and slot definitions.
// This is separate from the state machine (GraphModel / state.yml).

export interface PluginSlotDef {
  id: string;
  label?: string;
  type: 'text' | 'image' | 'file' | 'json';
  cardinality?: 'single' | 'list';
  ordered?: boolean;
  allow_manual_add?: boolean;
  summary_max_chars?: number;
}

export interface PluginUiTab {
  id: string;
  label?: string;
  layout?: 'list' | 'grid' | 'horizontal';
  slots: Array<{ id: string }>;
}

export interface PluginToolScript {
  path: string;
  functions: string[];
}

export interface PluginModel {
  id: string;
  name: string;
  description?: string;
  when_to_use?: string;
  tool_scripts?: PluginToolScript[];
  /** Step metadata only (id + label). Execution details live in state.yml / GraphModel. */
  steps: Array<{ id: string; label: string }>;
  /** Slot definitions — list format, each entry is a complete PluginSlotDef. */
  slots: PluginSlotDef[];
  ui?: { tabs: PluginUiTab[] };
  /** i18n block is preserved as-is; never shown or edited in the UI. */
  i18n?: Record<string, unknown>;
}

export const createEmptyPluginModel = (): PluginModel => ({
  id: '',
  name: '',
  steps: [],
  slots: [],
});
