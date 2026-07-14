import jsYaml from 'js-yaml';
import type { PluginModel } from './pluginModel';
import type { GraphModel } from './model';

/**
 * Serialize a PluginModel back to a canonical plugin.yaml YAML string.
 * Slots are sourced from GraphModel.slots (single source of truth) rather than
 * PluginModel.slots (which is no longer maintained).
 * i18n block is preserved as-is.
 */
export function serializePluginModel(model: PluginModel, graphModel?: GraphModel): string {
  const doc: Record<string, unknown> = {};

  doc.id = model.id;
  doc.name = model.name;
  if (model.description) doc.description = model.description;
  if (model.when_to_use) doc.when_to_use = model.when_to_use;

  if (model.tool_scripts && model.tool_scripts.length > 0) {
    doc.tool_scripts = model.tool_scripts.map((ts) => ({
      path: ts.path,
      functions: ts.functions,
    }));
  }

  // state.yml / GraphModel is authoritative for the step list. This keeps
  // plugin.yaml declarations correct for canvas renames and direct state.yml
  // edits, even if PluginModel was initialized from an incomplete draft.
  const stepsSource = graphModel
    ? graphModel.nodes.map((node) => ({ id: node.id, label: node.label }))
    : model.steps;
  if (stepsSource.length > 0) {
    doc.steps = stepsSource.map((s) => ({ id: s.id, label: s.label }));
  }

  // Slots come from GraphModel.slots when available; fall back to PluginModel.slots for
  // backward-compatibility (e.g. when called without a graphModel).
  const slotsSource = graphModel
    ? Object.values(graphModel.slots)
    : model.slots;

  if (slotsSource.length > 0) {
    doc.slots = slotsSource.map((slot) => {
      const entry: Record<string, unknown> = { id: slot.id, type: slot.type };
      if (slot.label) entry.label = slot.label;
      if (slot.cardinality === 'list') {
        entry.cardinality = 'list';
        if (slot.ordered) entry.ordered = true;
        if (slot.allow_manual_add !== undefined) entry.allow_manual_add = slot.allow_manual_add;
      }
      if (slot.summary_max_chars != null) entry.summary_max_chars = slot.summary_max_chars;
      if (slot.external) entry.external = true;
      return entry;
    });
  }

  if (model.ui?.tabs && model.ui.tabs.length > 0) {
    const uiDoc: Record<string, unknown> = {};

    // Serialize global ui.slots
    if (model.ui.slots && Object.keys(model.ui.slots).length > 0) {
      uiDoc.slots = model.ui.slots;
    }

    uiDoc.tabs = model.ui.tabs.map((tab) => {
      const t: Record<string, unknown> = { id: tab.id };
      if (tab.label) t.label = tab.label;
      if (tab.layout) t.layout = tab.layout;
      if (tab.gridCols != null) t.grid_cols = tab.gridCols;
      // slots: only output id list
      t.slots = tab.slots.map((s) => ({ id: s.id }));
      if (tab.composite_tab_position) t.composite_tab_position = tab.composite_tab_position;
      if (tab.composite_layout != null) t.composite_layout = tab.composite_layout;
      return t;
    });

    doc.ui = uiDoc;
  }

  if (model.i18n) doc.i18n = model.i18n;

  return jsYaml.dump(doc, {
    indent: 2,
    lineWidth: 120,
    noRefs: true,
    quotingType: '"',
  });
}
