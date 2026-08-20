// Data model for workflow.yaml — the workflow metadata and slot definitions.
// This is separate from the state machine (GraphModel / state.yml).

export interface WorkflowSlotDef {
  id: string;
  label?: string;
  type: 'text' | 'image' | 'file' | 'json';
  cardinality?: 'single' | 'list';
  ordered?: boolean;
  allow_manual_add?: boolean;
  summary_max_chars?: number;
  external?: boolean;
}

// ── Widget type system ────────────────────────────────────────────────────────

export type WidgetType =
  | 'text-single'
  | 'text-list'
  | 'text-markdown'
  | 'html-slide'
  | 'image-single'
  | 'image-gallery'
  | 'file-card'
  | 'json-block';

/** Default widget for each slot type+cardinality combination. */
export const SLOT_DEFAULT_WIDGET: Record<string, WidgetType> = {
  'text/single':  'text-single',
  'text/list':    'text-list',
  'image/single': 'image-single',
  'image/list':   'image-gallery',
  'file/single':  'file-card',
  'file/list':    'file-card',
  'json/single':  'json-block',
  'json/list':    'json-block',
};

/** Compatible widget types for each slot type+cardinality. */
export const SLOT_COMPATIBLE_WIDGETS: Record<string, WidgetType[]> = {
  'text/single':  ['text-single', 'text-markdown', 'html-slide'],
  'text/list':    ['text-list', 'text-markdown', 'html-slide'],
  'image/single': ['image-single'],
  'image/list':   ['image-gallery'],
  'file/single':  ['file-card'],
  'file/list':    ['file-card'],
  'json/single':  ['json-block', 'text-single'],
  'json/list':    ['json-block', 'text-single'],
};

interface WidgetBaseConfig {
  widgetType: WidgetType;
  readOnly?: boolean;
  maxHeight?: number;
}

export interface TextSingleConfig extends WidgetBaseConfig {
  widgetType: 'text-single';
}

export interface TextListConfig extends WidgetBaseConfig {
  widgetType: 'text-list';
  itemLayout?: 'vertical' | 'horizontal' | 'grid';
  itemMaxWidth?: number;
  gridMaxCols?: number;
  showAddButton?: boolean;
}

export interface TextMarkdownConfig extends WidgetBaseConfig {
  widgetType: 'text-markdown';
}

export interface HtmlSlideConfig extends WidgetBaseConfig {
  widgetType: 'html-slide';
}

export interface ImageSingleConfig extends WidgetBaseConfig {
  widgetType: 'image-single';
  imageHeight?: number;
}

export interface ImageGalleryConfig extends WidgetBaseConfig {
  widgetType: 'image-gallery';
  itemLayout?: 'horizontal' | 'grid';
  itemWidth?: number;
  itemHeight?: number;
  gridMaxCols?: number;
  showAddButton?: boolean;
}

export interface FileCardConfig extends WidgetBaseConfig {
  widgetType: 'file-card';
}

export interface JsonBlockConfig extends WidgetBaseConfig {
  widgetType: 'json-block';
  collapsed?: boolean;
}

export type WidgetConfig =
  | TextSingleConfig
  | TextListConfig
  | TextMarkdownConfig
  | HtmlSlideConfig
  | ImageSingleConfig
  | ImageGalleryConfig
  | FileCardConfig
  | JsonBlockConfig;

// ── Composite layout tree (format C) ─────────────────────────────────────────

/** A single named tab inside a composite leaf pane. */
export interface CompositeTab {
  label: string;
  slot: string;
}

/**
 * Recursive tree node for composite_layout (format C).
 * Exactly one of { slot, tabs, direction+children } should be set.
 */
export interface CompositePanelNode {
  /** Leaf: single slot id. */
  slot?: string;
  /** Leaf: tab-switching area. */
  tabs?: CompositeTab[];
  /** Human-readable name for this block (optional). */
  label?: string;
  /** Container: split direction. */
  direction?: 'row' | 'column';
  children?: CompositePanelNode[];
  weight?: number;
}

/**
 * Return every slot referenced by a composite tree in visual traversal order.
 * The layout tree is the source of truth for a composite tab: keeping the tab's
 * flat `slots` list in sync is required by both validation and runtime widget
 * hydration.
 */
export function collectCompositeSlotIds(node?: CompositePanelNode): string[] {
  const ids: string[] = [];
  const seen = new Set<string>();
  const add = (slotId?: string) => {
    const normalized = slotId?.trim();
    if (!normalized || seen.has(normalized)) return;
    seen.add(normalized);
    ids.push(normalized);
  };
  const visit = (current?: CompositePanelNode) => {
    if (!current) return;
    add(current.slot);
    current.tabs?.forEach((tab) => add(tab.slot));
    current.children?.forEach(visit);
  };
  visit(node);
  return ids;
}

// ── Workflow UI model ───────────────────────────────────────────────────────────

export interface WorkflowUiTab {
  id: string;
  step_id?: string;
  label?: string;
  layout?: 'vertical' | 'grid' | 'horizontal' | 'composite';
  /** Number of columns in grid layout (undefined = auto-fill). */
  gridCols?: number;
  /** Slot id list only — widget config lives in ui.slots. */
  slots: Array<{ id: string }>;
  /** Composite mode: global tab-bar position. */
  composite_tab_position?: 'top' | 'bottom' | 'left' | 'right';
  /** Composite mode: layout tree (format C). */
  composite_layout?: CompositePanelNode;
  /** Generic composite display rules (hide empty columns, mutually exclusive groups). */
  composite_behavior?: CompositeBehavior;
  /** Provider-backed actions declared by this tab. */
  actions?: WorkflowTabAction[];
}

export interface WorkflowTabAction {
  id: string;
  type: 'export';
  provider: string;
  label?: string;
  inputs: Record<string, string>;
  formats?: string[];
  alignment?: 'sort_order';
}

/** Mutually exclusive column group for composite tabs. */
export interface CompositeMutuallyExclusiveGroup {
  slots: string[];
  prefer?: string[];
}

export interface CompositeBehavior {
  hide_empty_columns?: boolean;
  empty_column_scope?: 'selected' | 'tab';
  mutually_exclusive?: CompositeMutuallyExclusiveGroup[];
}

export interface WorkflowToolScript {
  path: string;
  functions: string[];
}

export interface WorkflowRuntimePolicy {
  publisher_owned_slots?: string[];
  collects_knowledge?: boolean;
  completed_edit_step?: string;
  clarification_fields?: Array<{
    id: string;
    label?: string;
    question: string;
    type?: 'text' | 'boolean' | 'single' | 'multiple';
    choices?: string[];
  }>;
}

export interface WorkflowModel {
  id: string;
  name: string;
  description?: string;
  when_to_use?: string;
  tool_scripts?: WorkflowToolScript[];
  /** Host-neutral execution policy; preserved even though the editor has no dedicated controls. */
  runtime?: WorkflowRuntimePolicy;
  /** Artifact actions are package contracts and are preserved as opaque declarations. */
  artifact_actions?: Record<string, unknown>;
  /** Step metadata only (id + label). Execution details live in state.yml / GraphModel. */
  steps: Array<{ id: string; label: string }>;
  /** Slot definitions — list format, each entry is a complete WorkflowSlotDef. */
  slots: WorkflowSlotDef[];
  ui?: {
    tabs: WorkflowUiTab[];
    /** Global widget config keyed by slot id. Shared across all tabs. */
    slots?: Record<string, WidgetConfig>;
  };
  /** i18n block is preserved as-is; never shown or edited in the UI. */
  i18n?: Record<string, unknown>;
}

export const createEmptyWorkflowModel = (): WorkflowModel => ({
  id: '',
  name: '',
  steps: [],
  slots: [],
});
