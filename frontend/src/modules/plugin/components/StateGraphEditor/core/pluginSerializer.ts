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

  if (model.steps.length > 0) {
    doc.steps = model.steps.map((s) => ({ id: s.id, label: s.label }));
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
      return entry;
    });
  }

  if (model.ui?.tabs && model.ui.tabs.length > 0) {
    doc.ui = {
      tabs: model.ui.tabs.map((tab) => {
        const t: Record<string, unknown> = { id: tab.id };
        if (tab.label) t.label = tab.label;
        if (tab.layout) t.layout = tab.layout;
        t.slots = tab.slots.map((s) => ({ id: s.id }));
        return t;
      }),
    };
  }

  if (model.i18n) doc.i18n = model.i18n;

  return jsYaml.dump(doc, {
    indent: 2,
    lineWidth: 120,
    noRefs: true,
    quotingType: '"',
  });
}
