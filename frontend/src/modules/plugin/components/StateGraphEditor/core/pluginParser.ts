import jsYaml from 'js-yaml';
import type { PluginModel, PluginSlotDef, PluginToolScript, PluginUiTab } from './pluginModel';

interface RawPluginYaml {
  id?: unknown;
  name?: unknown;
  description?: unknown;
  when_to_use?: unknown;
  tool_scripts?: unknown;
  steps?: unknown;
  slots?: unknown;
  ui?: unknown;
  i18n?: unknown;
}

function parseSlots(raw: unknown): PluginSlotDef[] {
  if (!raw) return [];
  // New format: array of objects with an 'id' field.
  if (Array.isArray(raw)) {
    return raw.flatMap((item): PluginSlotDef[] => {
      if (!item || typeof item !== 'object' || Array.isArray(item)) return [];
      const entry = item as Record<string, unknown>;
      const id = String(entry.id ?? '').trim();
      if (!id) return [];
      const slot: PluginSlotDef = {
        id,
        type: ['text', 'image', 'file', 'json'].includes(String(entry.type)) ? (String(entry.type) as PluginSlotDef['type']) : 'text',
        label: entry.label !== undefined ? String(entry.label) : undefined,
      };
      if (entry.cardinality === 'list') {
        slot.cardinality = 'list';
        if (entry.ordered === true || entry.ordered === 'true') slot.ordered = true;
        if (entry.allow_manual_add === false || entry.allow_manual_add === 'false') slot.allow_manual_add = false;
        if (entry.allow_manual_add === true || entry.allow_manual_add === 'true') slot.allow_manual_add = true;
      }
      if (typeof entry.summary_max_chars === 'number' && entry.summary_max_chars > 0) {
        slot.summary_max_chars = entry.summary_max_chars;
      }
      return [slot];
    });
  }
  // Legacy map format: { slot_id: { type, label, ... } }
  if (typeof raw === 'object' && !Array.isArray(raw)) {
    return Object.entries(raw as Record<string, unknown>).flatMap(([id, val]): PluginSlotDef[] => {
      const entry = val && typeof val === 'object' && !Array.isArray(val) ? (val as Record<string, unknown>) : {};
      const slot: PluginSlotDef = {
        id,
        type: ['text', 'image', 'file', 'json'].includes(String(entry.type)) ? (String(entry.type) as PluginSlotDef['type']) : 'text',
        label: entry.label !== undefined ? String(entry.label) : undefined,
      };
      if (entry.cardinality === 'list') {
        slot.cardinality = 'list';
        if (entry.ordered === true || entry.ordered === 'true') slot.ordered = true;
        if (entry.allow_manual_add === false || entry.allow_manual_add === 'false') slot.allow_manual_add = false;
        if (entry.allow_manual_add === true || entry.allow_manual_add === 'true') slot.allow_manual_add = true;
      }
      if (typeof entry.summary_max_chars === 'number' && entry.summary_max_chars > 0) {
        slot.summary_max_chars = entry.summary_max_chars;
      }
      return [slot];
    });
  }
  return [];
}

function parseToolScripts(raw: unknown): PluginToolScript[] {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((item): PluginToolScript[] => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) return [];
    const entry = item as Record<string, unknown>;
    const path = String(entry.path ?? '').trim();
    if (!path) return [];
    const functions = Array.isArray(entry.functions) ? entry.functions.map(String) : [];
    return [{ path, functions }];
  });
}

function parseUiTabs(raw: unknown): PluginUiTab[] | undefined {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return undefined;
  const uiObj = raw as Record<string, unknown>;
  if (!Array.isArray(uiObj.tabs)) return undefined;
  return uiObj.tabs.flatMap((tab): PluginUiTab[] => {
    if (!tab || typeof tab !== 'object' || Array.isArray(tab)) return [];
    const t = tab as Record<string, unknown>;
    const id = String(t.id ?? '').trim();
    if (!id) return [];
    return [{
      id,
      label: t.label !== undefined ? String(t.label) : undefined,
      layout: ['list', 'grid', 'horizontal'].includes(String(t.layout)) ? (String(t.layout) as PluginUiTab['layout']) : undefined,
      slots: Array.isArray(t.slots) ? t.slots.map((s: unknown) => ({ id: String(typeof s === 'object' && s !== null ? (s as Record<string, unknown>).id ?? s : s) })) : [],
    }];
  });
}

function parseSteps(raw: unknown): Array<{ id: string; label: string }> {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((item): Array<{ id: string; label: string }> => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) return [];
    const entry = item as Record<string, unknown>;
    const id = String(entry.id ?? '').trim();
    if (!id) return [];
    return [{ id, label: String(entry.label ?? id) }];
  });
}

/**
 * Parse a plugin.yaml string into a PluginModel.
 * Returns null on YAML syntax errors.
 */
export function parsePluginYaml(yamlText: string): PluginModel | null {
  let raw: RawPluginYaml;
  try {
    raw = (jsYaml.load(yamlText) ?? {}) as RawPluginYaml;
  } catch {
    return null;
  }

  const uiTabs = parseUiTabs(raw.ui);

  return {
    id: String(raw.id ?? ''),
    name: String(raw.name ?? ''),
    description: raw.description !== undefined ? String(raw.description) : undefined,
    when_to_use: raw.when_to_use !== undefined ? String(raw.when_to_use) : undefined,
    tool_scripts: parseToolScripts(raw.tool_scripts),
    steps: parseSteps(raw.steps),
    slots: parseSlots(raw.slots),
    ui: uiTabs ? { tabs: uiTabs } : undefined,
    i18n: raw.i18n as Record<string, unknown> | undefined,
  };
}
