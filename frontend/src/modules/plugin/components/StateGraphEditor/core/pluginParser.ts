import jsYaml from 'js-yaml';
import type { PluginModel, PluginSlotDef, PluginToolScript, PluginUiTab, WidgetConfig, CompositePanelNode, CompositeTab } from './pluginModel';

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

/**
 * Migrate legacy composite_layout (format A or B) to format C (CompositePanelNode tree).
 * Format A: layout[0] is an array of columns — [[{slot, weight}, ...]] or [[slotId, ...]]
 * Format B: flat array — [{slot, weight}, ...]
 */
function migrateLegacyCompositeLayout(raw: unknown): CompositePanelNode {
  if (!Array.isArray(raw) || raw.length === 0) {
    return { direction: 'row', children: [] };
  }
  // Format A: first element is an array
  const cols: unknown[] = Array.isArray(raw[0]) ? (raw[0] as unknown[]) : raw;
  const children: CompositePanelNode[] = cols.map((node) => {
    if (typeof node === 'string') return { slot: node, weight: 1 };
    if (typeof node === 'object' && node !== null) {
      const n = node as Record<string, unknown>;
      // { slot: { tabs: [...] }, weight } -> { tabs: [...], weight }
      if (n.slot && typeof n.slot === 'object' && !Array.isArray(n.slot) && 'tabs' in (n.slot as object)) {
        const tabsRaw = (n.slot as Record<string, unknown>).tabs;
        const tabIds = Array.isArray(tabsRaw) ? tabsRaw.map((t) => typeof t === 'string' ? t : String(t)) : [];
        const tabs: CompositeTab[] = tabIds.map((id, idx) => ({ label: `Tab ${idx + 1}`, slot: id }));
        return { tabs, weight: typeof n.weight === 'number' ? n.weight : 1 };
      }
      const slotId = typeof n.slot === 'string' ? n.slot : '';
      return { slot: slotId, weight: typeof n.weight === 'number' ? n.weight : 1 };
    }
    return { slot: '', weight: 1 };
  });
  return { direction: 'row', children };
}

/** Migrate CompositeTab fields: if tabs is string[], convert to CompositeTab[]. */
function migrateCompositeTabs(node: CompositePanelNode): CompositePanelNode {
  const migrated: CompositePanelNode = { ...node };
  if (Array.isArray(node.tabs)) {
    const rawTabs = node.tabs as unknown[];
    if (rawTabs.length > 0 && typeof rawTabs[0] === 'string') {
      migrated.tabs = (rawTabs as string[]).map((id, idx) => ({ label: `Tab ${idx + 1}`, slot: id }));
    }
  }
  if (node.children) {
    migrated.children = node.children.map(migrateCompositeTabs);
  }
  return migrated;
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
      if (entry.external === true || entry.external === 'true' || entry.producer === 'external') {
        slot.external = true;
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
      if (entry.external === true || entry.external === 'true' || entry.producer === 'external') {
        slot.external = true;
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

function parseUiTabs(raw: unknown): { tabs: PluginUiTab[]; slots?: Record<string, WidgetConfig> } | undefined {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return undefined;
  const uiObj = raw as Record<string, unknown>;
  if (!Array.isArray(uiObj.tabs)) return undefined;

  // Parse global ui.slots map
  let uiSlots: Record<string, WidgetConfig> | undefined;
  if (uiObj.slots && typeof uiObj.slots === 'object' && !Array.isArray(uiObj.slots)) {
    uiSlots = {};
    for (const [slotId, widgetRaw] of Object.entries(uiObj.slots as Record<string, unknown>)) {
      if (widgetRaw && typeof widgetRaw === 'object' && !Array.isArray(widgetRaw)) {
        uiSlots[slotId] = widgetRaw as WidgetConfig;
      }
    }
  }

  const tabs = uiObj.tabs.flatMap((tab): PluginUiTab[] => {
    if (!tab || typeof tab !== 'object' || Array.isArray(tab)) return [];
    const t = tab as Record<string, unknown>;
    const id = String(t.id ?? '').trim();
    if (!id) return [];

    const validLayouts = ['list', 'vertical', 'grid', 'horizontal', 'composite'];
    const rawLayout = String(t.layout ?? '');
    const layout = validLayouts.includes(rawLayout) ? (rawLayout as PluginUiTab['layout']) : undefined;

    // Parse slots — only id, widget is migrated to ui.slots
    const slots = Array.isArray(t.slots)
      ? t.slots.flatMap((s: unknown): Array<{ id: string }> => {
          if (!s || typeof s !== 'object' || Array.isArray(s)) {
            const sid = String(s ?? '').trim();
            return sid ? [{ id: sid }] : [];
          }
          const se = s as Record<string, unknown>;
          const slotId = String(se.id ?? '').trim();
          if (!slotId) return [];
          // Migrate legacy per-slot widget into uiSlots
          if (se.widget && typeof se.widget === 'object' && !Array.isArray(se.widget)) {
            if (!uiSlots) uiSlots = {};
            if (!uiSlots[slotId]) {
              uiSlots[slotId] = se.widget as WidgetConfig;
            }
          }
          return [{ id: slotId }];
        })
      : [];

    // Parse composite_layout: migrate array format to format C
    let compositeLayout: CompositePanelNode | undefined;
    if (t.composite_layout !== undefined && t.composite_layout !== null) {
      if (Array.isArray(t.composite_layout)) {
        compositeLayout = migrateLegacyCompositeLayout(t.composite_layout);
      } else if (typeof t.composite_layout === 'object' && 'direction' in (t.composite_layout as object)) {
        compositeLayout = migrateCompositeTabs(t.composite_layout as CompositePanelNode);
      }
    }

    const validTabPositions = ['top', 'bottom', 'left', 'right'];
    const rawTabPos = String(t.composite_tab_position ?? '');
    const compositeTabPosition = validTabPositions.includes(rawTabPos)
      ? (rawTabPos as PluginUiTab['composite_tab_position'])
      : undefined;

    return [{
      id,
      label: t.label !== undefined ? String(t.label) : undefined,
      layout,
      gridCols: typeof t.grid_cols === 'number' ? t.grid_cols : undefined,
      slots,
      composite_layout: compositeLayout,
      composite_tab_position: compositeTabPosition,
    }];
  });

  return { tabs, slots: uiSlots };
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

  const uiResult = parseUiTabs(raw.ui);

  return {
    id: String(raw.id ?? ''),
    name: String(raw.name ?? ''),
    description: raw.description !== undefined ? String(raw.description) : undefined,
    when_to_use: raw.when_to_use !== undefined ? String(raw.when_to_use) : undefined,
    tool_scripts: parseToolScripts(raw.tool_scripts),
    steps: parseSteps(raw.steps),
    slots: parseSlots(raw.slots),
    ui: uiResult ? { tabs: uiResult.tabs, slots: uiResult.slots } : undefined,
    i18n: raw.i18n as Record<string, unknown> | undefined,
  };
}
