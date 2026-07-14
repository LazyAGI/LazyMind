import { useState } from 'react';
import { Button, Checkbox, Input, InputNumber, Select, Tooltip, Empty, Dropdown, Popconfirm } from 'antd';
import { PlusOutlined, CloseOutlined, CheckOutlined, DownOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { SlotDef, GraphModel } from '../core/model';
import { removeMaterialFromExpression } from '../core/model';
import type { PluginModel, PluginUiTab, WidgetConfig, WidgetType, CompositePanelNode } from '../core/pluginModel';
import { SLOT_DEFAULT_WIDGET, SLOT_COMPATIBLE_WIDGETS } from '../core/pluginModel';
import WidgetSelector from '../UiEditorPanel/WidgetSelector';
import './index.scss';

const ARTIFACT_ID_REGEX = /^[a-zA-Z0-9_]+$/;

const TYPE_VALUES = ['text', 'image', 'file', 'json'] as const;

const TYPE_LABEL_KEYS: Record<string, string> = {
  text: 'selfEvolutionRun.stateGraphArtifactTypeText',
  image: 'selfEvolutionRun.stateGraphArtifactTypeImage',
  file: 'selfEvolutionRun.stateGraphArtifactTypeFile',
  json: 'selfEvolutionRun.stateGraphArtifactTypeJson',
};

interface Props {
  model: GraphModel;
  onClose: () => void;
  /** Accepts a functional updater so callers always operate on the latest model,
   *  avoiding stale-closure overwrites when layout/width was updated since last render. */
  onModelChange: (updater: (prev: GraphModel) => GraphModel) => void;
  uiMode?: boolean;
  /** When true, renders as an inline block (position: static) instead of the default floating overlay */
  inline?: boolean;
  pluginModel?: PluginModel;
  activeTabId?: string;
  onUiModelChange?: (ui: PluginModel['ui']) => void;
  /** Navigate to the tab where a slot lives. */
  onTabNavigate?: (tabId: string) => void;
  /** When true, hide all editing controls. */
  readonly?: boolean;
}

interface EditDraft {
  id: string;
  label: string;
  type: string;
  cardinality: 'single' | 'list';
  ordered: boolean;
  allow_manual_add: boolean;
  external: boolean;
  summary_max_chars: string;
  idError?: string;
}

const EMPTY_DRAFT: EditDraft = {
  id: '',
  label: '',
  type: 'text',
  cardinality: 'single',
  ordered: false,
  allow_manual_add: true,
  external: false,
  summary_max_chars: '',
};

/** Returns true if any step node uses slotId as an input. */
function isUsedAsInput(model: GraphModel, slotId: string): boolean {
  return model.nodes.some((n) =>
    n.inputs.some((input) => input.material === slotId || input.alternatives?.includes(slotId)),
  );
}

// ── Composite layout helpers ─────────────────────────────────────────────────

/** Describes where a slot lives within the plugin UI. */
type SlotLocation =
  | { kind: 'simple'; tabId: string; tabLabel: string }
  | { kind: 'composite-block'; tabId: string; tabLabel: string; blockPath: number[]; blockLabel?: string }
  | {
      kind: 'composite-tab';
      tabId: string;
      tabLabel: string;
      blockPath: number[];
      blockLabel?: string;
      tabIdx: number;
      innerTabLabel: string;
    };

/** Walk a composite tree and find the path where slotId lives. */
function findInComposite(
  node: CompositePanelNode,
  slotId: string,
  path: number[] = [],
): { path: number[]; tabIdx?: number } | null {
  if (node.slot === slotId) return { path };
  if (node.tabs) {
    const idx = node.tabs.findIndex((t) => t.slot === slotId);
    if (idx >= 0) return { path, tabIdx: idx };
  }
  if (node.children) {
    for (let i = 0; i < node.children.length; i++) {
      const found = findInComposite(node.children[i], slotId, [...path, i]);
      if (found) return found;
    }
  }
  return null;
}

/** Get the block node at a given path in the composite tree. */
function getNodeAtPath(root: CompositePanelNode, path: number[]): CompositePanelNode | null {
  let cur: CompositePanelNode = root;
  for (const idx of path) {
    if (!cur.children?.[idx]) return null;
    cur = cur.children[idx];
  }
  return cur;
}

/** Find where a slot lives across all PluginUiTabs. */
function findSlotLocation(slotId: string, tabs: PluginUiTab[]): SlotLocation | null {
  for (const tab of tabs) {
    // Simple (non-composite) tabs
    if (tab.layout !== 'composite') {
      if (tab.slots.some((s) => s.id === slotId)) {
        return { kind: 'simple', tabId: tab.id, tabLabel: tab.label ?? tab.id };
      }
      continue;
    }
    // Composite tab
    if (!tab.composite_layout) continue;
    const found = findInComposite(tab.composite_layout, slotId);
    if (!found) continue;
    const blockNode = getNodeAtPath(tab.composite_layout, found.path);
    const blockLabel = blockNode?.label;
    if (found.tabIdx !== undefined) {
      const innerTabLabel = blockNode?.tabs?.[found.tabIdx]?.label ?? `Tab ${found.tabIdx + 1}`;
      return {
        kind: 'composite-tab',
        tabId: tab.id,
        tabLabel: tab.label ?? tab.id,
        blockPath: found.path,
        blockLabel,
        tabIdx: found.tabIdx,
        innerTabLabel,
      };
    }
    return {
      kind: 'composite-block',
      tabId: tab.id,
      tabLabel: tab.label ?? tab.id,
      blockPath: found.path,
      blockLabel,
    };
  }
  return null;
}

/** Format a location as a human-readable string. */
function formatLocation(loc: SlotLocation): string {
  if (loc.kind === 'simple') return loc.tabLabel;
  const parts: string[] = [loc.tabLabel];
  if (loc.blockLabel) parts.push(loc.blockLabel);
  if (loc.kind === 'composite-tab') parts.push(loc.innerTabLabel);
  return parts.join(' › ');
}

/** Remove a slot from the composite tree immutably. */
function removeFromComposite(node: CompositePanelNode, slotId: string): CompositePanelNode {
  if (node.slot === slotId) return { ...node, slot: '' };
  if (node.tabs) {
    if (node.tabs.some((t) => t.slot === slotId)) {
      return { ...node, tabs: node.tabs.map((t) => t.slot === slotId ? { ...t, slot: '' } : t) };
    }
  }
  if (node.children) {
    return { ...node, children: node.children.map((c) => removeFromComposite(c, slotId)) };
  }
  return node;
}

/** Assign a slot to a position in the composite tree immutably.
 *  blockPath: path to the leaf node; tabIdx: if set, assign to that tab slot; else assign to node.slot.
 */
function assignInComposite(
  node: CompositePanelNode,
  blockPath: number[],
  tabIdx: number | undefined,
  slotId: string,
): CompositePanelNode {
  if (blockPath.length === 0) {
    if (tabIdx !== undefined && node.tabs) {
      const newTabs = node.tabs.map((t, i) => i === tabIdx ? { ...t, slot: slotId } : t);
      return { ...node, tabs: newTabs };
    }
    return { ...node, slot: slotId };
  }
  if (!node.children) return node;
  const [head, ...rest] = blockPath;
  const newChildren = node.children.map((c, i) =>
    i === head ? assignInComposite(c, rest, tabIdx, slotId) : c,
  );
  return { ...node, children: newChildren };
}

// ── Assignment target descriptors (for the cascade dropdown) ─────────────────

interface AssignTarget {
  key: string;
  label: string;
  tabId: string;
  isComposite: boolean;
  blockPath?: number[];
  tabIdx?: number;
  /** For composite tabs: the cardinality constraint derived from already-assigned slots.
   *  undefined = no slots assigned yet (no constraint). */
  listConstraint?: 'list' | 'single';
}

/** Collect all used slot ids and their paths from a composite tree. */
function collectBoundSlots(node: CompositePanelNode): string[] {
  const ids: string[] = [];
  function walk(n: CompositePanelNode) {
    if (n.slot) ids.push(n.slot);
    if (n.tabs) n.tabs.forEach((t) => { if (t.slot) ids.push(t.slot); });
    if (n.children) n.children.forEach(walk);
  }
  walk(node);
  return ids;
}

/** Collect all assignable positions from a PluginUiTab list as flat entries. */
function collectAssignTargets(tabs: PluginUiTab[], slotMap: Record<string, SlotDef>, blockLabelFallback: (path: number[]) => string): AssignTarget[] {
  const targets: AssignTarget[] = [];

  for (const tab of tabs) {
    if (tab.layout !== 'composite') {
      targets.push({
        key: tab.id,
        label: tab.label ?? tab.id,
        tabId: tab.id,
        isComposite: false,
      });
    } else if (tab.composite_layout?.direction) {
      // Compute list constraint from already-bound slots in this composite tab
      const boundIds = collectBoundSlots(tab.composite_layout);
      let tabListConstraint: 'list' | 'single' | undefined;
      for (const sid of boundIds) {
        const def = slotMap[sid];
        if (def) {
          const c: 'list' | 'single' = def.cardinality === 'list' ? 'list' : 'single';
          if (tabListConstraint === undefined) {
            tabListConstraint = c;
          }
          // Once we have at least one bound slot, the constraint is set
          break;
        }
      }

      function walkNode(
        node: CompositePanelNode,
        path: number[],
        tabId: string,
        tabLabel: string,
        depth: number,
      ) {
        const isLeaf = !node.direction && !node.children?.length;
        if (!isLeaf) {
          (node.children ?? []).forEach((c, i) => walkNode(c, [...path, i], tabId, tabLabel, depth + 1));
          return;
        }
        const blockLabel = node.label ?? blockLabelFallback(path);
        if (Array.isArray(node.tabs) && node.tabs.length > 0) {
          node.tabs.forEach((t, idx) => {
            targets.push({
              key: `${tabId}::${path.join('/')}::tab::${idx}`,
              label: `${tabLabel} › ${blockLabel} › ${t.label}`,
              tabId,
              isComposite: true,
              blockPath: path,
              tabIdx: idx,
              listConstraint: tabListConstraint,
            });
          });
        } else {
          targets.push({
            key: `${tabId}::${path.join('/')}`,
            label: `${tabLabel} › ${blockLabel}`,
            tabId,
            isComposite: true,
            blockPath: path,
            listConstraint: tabListConstraint,
          });
        }
      }

      walkNode(tab.composite_layout, [], tab.id, tab.label ?? tab.id, 0);
    }
  }

  return targets;
}

// ── EditForm ────────────────────────────────────────────────────────────────
interface EditFormProps {
  draft: EditDraft;
  isNew: boolean;
  onChange: (patch: Partial<EditDraft>) => void;
  onSave: () => void;
  onCancel: () => void;
  saveLabel?: string;
}

function EditForm({ draft, isNew, onChange, onSave, onCancel, saveLabel }: EditFormProps) {
  const { t } = useTranslation();
  const typeOptions = TYPE_VALUES.map((v) => ({ label: t(TYPE_LABEL_KEYS[v]), value: v }));
  const resolvedSaveLabel = saveLabel ?? t('selfEvolutionRun.artifactPanelSave');
  return (
    <div className="artifact-edit-form">
      <div className="artifact-edit-row">
        <span className="artifact-edit-field-label">{t('selfEvolutionRun.artifactPanelFieldId')}</span>
        {isNew ? (
          <div className="artifact-edit-field-value">
            <Input
              size="small"
              value={draft.id}
              onChange={(e) => onChange({ id: e.target.value, idError: undefined })}
              placeholder={t('selfEvolutionRun.artifactPanelFieldIdPlaceholder')}
              status={draft.idError ? 'error' : ''}
              onPressEnter={onSave}
              autoFocus
            />
            {draft.idError && <div className="artifact-id-error">{draft.idError}</div>}
          </div>
        ) : (
          <span className="artifact-edit-id-readonly">{draft.id}</span>
        )}
      </div>
      <div className="artifact-edit-row">
        <span className="artifact-edit-field-label">{t('selfEvolutionRun.artifactPanelFieldLabel')}</span>
        <Input
          size="small"
          value={draft.label}
          onChange={(e) => onChange({ label: e.target.value })}
          placeholder={t('selfEvolutionRun.artifactPanelFieldLabelPlaceholder')}
          className="artifact-edit-field-value"
        />
      </div>
      <div className="artifact-edit-row">
        <span className="artifact-edit-field-label">{t('selfEvolutionRun.artifactPanelFieldType')}</span>
        <Select
          size="small"
          value={draft.type}
          options={typeOptions}
          onChange={(val) => onChange({ type: val })}
          className="artifact-edit-type-select"
        />
      </div>
      <div className="artifact-edit-row artifact-edit-row--flags">
        <Checkbox
          checked={draft.external}
          onChange={(e) => onChange({ external: e.target.checked })}
        >
          外部输入
        </Checkbox>
        <Checkbox
          checked={draft.cardinality === 'list'}
          onChange={(e) => onChange({ cardinality: e.target.checked ? 'list' : 'single' })}
        >
          {t('selfEvolutionRun.artifactPanelFieldIsList')}
        </Checkbox>
        {draft.cardinality === 'list' && (
          <>
            <Checkbox
              checked={draft.ordered}
              onChange={(e) => onChange({ ordered: e.target.checked })}
            >
              {t('selfEvolutionRun.artifactPanelFieldOrdered')}
            </Checkbox>
            <Checkbox
              checked={draft.allow_manual_add}
              onChange={(e) => onChange({ allow_manual_add: e.target.checked })}
            >
              {t('selfEvolutionRun.artifactPanelFieldAllowManualAdd')}
            </Checkbox>
          </>
        )}
      </div>
      <div className="artifact-edit-row">
        <span className="artifact-edit-field-label">{t('selfEvolutionRun.artifactPanelFieldSummaryMax')}</span>
        <InputNumber
          size="small"
          min={0}
          value={draft.summary_max_chars ? parseInt(draft.summary_max_chars, 10) : null}
          onChange={(val) => onChange({ summary_max_chars: val != null ? String(val) : '' })}
          placeholder={t('selfEvolutionRun.artifactPanelFieldSummaryMaxPlaceholder')}
          className="artifact-edit-summary-input"
        />
      </div>
      <div className="artifact-edit-actions">
        <Button size="small" type="primary" onClick={onSave}>{resolvedSaveLabel}</Button>
        <Button size="small" onClick={onCancel}>{t('selfEvolutionRun.artifactPanelCancel')}</Button>
      </div>
    </div>
  );
}

// ── ArtifactRow (preview + inline edit) ─────────────────────────────────────
interface ArtifactRowProps {
  art: SlotDef;
  model: GraphModel;
  uiMode?: boolean;
  tabs: PluginUiTab[];
  uiSlots: Record<string, WidgetConfig>;
  slotMap: Record<string, SlotDef>;
  onUpdate: (id: string, patch: Partial<Omit<SlotDef, 'id'>>) => void;
  onDelete: (id: string) => void;
  onAssign: (target: AssignTarget, slotId: string, widget: WidgetConfig) => void;
  onRemoveFromUi: (slotId: string) => void;
  onWidgetChange: (slotId: string, widget: WidgetConfig) => void;
  onTabNavigate?: (tabId: string) => void;
  readonly?: boolean;
}

function ArtifactRow({ art, model, uiMode, tabs, uiSlots, slotMap, onUpdate, onDelete, onAssign, onRemoveFromUi, onWidgetChange, onTabNavigate, readonly = false }: ArtifactRowProps) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<EditDraft>(EMPTY_DRAFT);

  const slotKey = `${art.type}/${art.cardinality ?? 'single'}`;
  const defaultWidgetType: WidgetType = (SLOT_DEFAULT_WIDGET[slotKey] ?? 'text-single') as WidgetType;
  const [selectedWidget, setSelectedWidget] = useState<WidgetType>(defaultWidgetType);

  const resolveAllowManualAdd = (): boolean => {
    if (art.allow_manual_add !== undefined) return art.allow_manual_add;
    return isUsedAsInput(model, art.id);
  };

  const currentLocation = findSlotLocation(art.id, tabs);

  const startEdit = () => {
    setDraft({
      id: art.id,
      label: art.label ?? '',
      type: art.type,
      cardinality: art.cardinality === 'list' ? 'list' : 'single',
      ordered: !!art.ordered,
      allow_manual_add: resolveAllowManualAdd(),
      external: !!art.external,
      summary_max_chars: art.summary_max_chars != null ? String(art.summary_max_chars) : '',
    });
    setEditing(true);
  };

  const handleSave = () => {
    const isList = draft.cardinality === 'list';
    const maxChars = parseInt(draft.summary_max_chars, 10);
    onUpdate(art.id, {
      type: draft.type,
      label: draft.label || undefined,
      cardinality: isList ? 'list' : undefined,
      ordered: (isList && draft.ordered) ? true : undefined,
      allow_manual_add: isList ? draft.allow_manual_add : undefined,
      external: draft.external || undefined,
      summary_max_chars: (!isNaN(maxChars) && maxChars > 0) ? maxChars : undefined,
    });
    setEditing(false);
  };

  const typeLabel = t(TYPE_LABEL_KEYS[art.type] ?? 'selfEvolutionRun.stateGraphArtifactTypeText');
  const cardinalityLabel = art.cardinality === 'list' ? `(${t('selfEvolutionRun.artifactPanelFieldIsList')})` : '';
  const displayName = art.label || art.id;
  const idLabel = art.label ? `(${art.id})` : '';
  const resolvedAllowManualAdd = art.cardinality === 'list'
    ? (art.allow_manual_add !== undefined ? art.allow_manual_add : isUsedAsInput(model, art.id))
    : false;

  if (editing) {
    return (
      <div className="artifact-item artifact-item--editing">
        <EditForm
          draft={draft}
          isNew={false}
          onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))}
          onSave={handleSave}
          onCancel={() => setEditing(false)}
          saveLabel={t('selfEvolutionRun.artifactPanelSave')}
        />
      </div>
    );
  }

  const handleDragStart = (e: React.DragEvent) => {
    e.dataTransfer.setData('application/x-slot-id', art.id);
    e.dataTransfer.setData('application/x-widget-type', selectedWidget);
    e.dataTransfer.effectAllowed = 'copy';
  };

  const compatibleWidgets = SLOT_COMPATIBLE_WIDGETS[slotKey] ?? ['text-single'];
  const assignTargets = collectAssignTargets(tabs, slotMap, (path) => t('selfEvolutionRun.artifactBlockLabel', { path: path.map((p) => p + 1).join('-') || '' }));
  const thisCardinality: 'list' | 'single' = art.cardinality === 'list' ? 'list' : 'single';

  return (
    <div
      className="artifact-item"
      draggable={uiMode}
      onDragStart={uiMode ? handleDragStart : undefined}
    >
      <div className="artifact-item-line1">
        {resolvedAllowManualAdd && (
          <span className="artifact-item-icon" title={t('selfEvolutionRun.artifactPanelAllowManualAddTitle')}>👤</span>
        )}
        <span className="artifact-item-name">{displayName}</span>
        {idLabel && <span className="artifact-item-id">{idLabel}</span>}
        <span className="artifact-item-sep">,</span>
        <span className="artifact-item-type">
          {typeLabel}
          {cardinalityLabel && <span className="artifact-item-cardinality">{cardinalityLabel}</span>}
        </span>
        <div className="artifact-item-actions">
          {!readonly && (
            <Button size="small" type="text" className="artifact-item-edit-btn" onClick={startEdit}>
              {t('selfEvolutionRun.artifactPanelEdit')}
            </Button>
          )}
          {!uiMode && !readonly && (
            <Popconfirm
              title={t('selfEvolutionRun.artifactPanelDeleteConfirm', { id: art.id })}
              onConfirm={() => onDelete(art.id)}
              okText={t('selfEvolutionRun.artifactPanelDeleteOk')}
              cancelText={t('selfEvolutionRun.artifactPanelDeleteCancel')}
              okButtonProps={{ danger: true }}
            >
              <Tooltip title={t('selfEvolutionRun.artifactPanelDeleteTooltip')}>
                <Button
                  type="text"
                  danger
                  size="small"
                  className="artifact-item-delete-btn"
                  aria-label={t('selfEvolutionRun.artifactPanelDeleteTooltip')}
                >
                  🗑️
                </Button>
              </Tooltip>
            </Popconfirm>
          )}
        </div>
      </div>
      {uiMode && (
        <div className="artifact-item-line2">
          {compatibleWidgets.length > 1 && !currentLocation && (
            <WidgetSelector
              slotType={art.type}
              cardinality={art.cardinality}
              value={selectedWidget}
              onChange={(wt) => setSelectedWidget(wt)}
              size="small"
            />
          )}
          {compatibleWidgets.length > 1 && currentLocation && (
            <WidgetSelector
              slotType={art.type}
              cardinality={art.cardinality}
              value={uiSlots[art.id]?.widgetType as WidgetType | undefined}
              onChange={(wt) => onWidgetChange(art.id, { widgetType: wt } as WidgetConfig)}
              size="small"
            />
          )}
          {currentLocation ? (
            <div className="artifact-row-joined">
              <button
                type="button"
                className="artifact-row-joined-label"
                onClick={() => onTabNavigate?.(currentLocation.tabId)}
                title={t('selfEvolutionRun.artifactJoinedTabNavigate')}
              >
                <CheckOutlined className="artifact-row-joined-check" />
                {t('selfEvolutionRun.artifactJoinedLabel', { location: formatLocation(currentLocation) })}
              </button>
              <Popconfirm
              title={t('selfEvolutionRun.artifactRemoveTitle')}
                description={t('selfEvolutionRun.artifactRemoveDesc')}
                onConfirm={() => onRemoveFromUi(art.id)}
                okText={t('selfEvolutionRun.artifactRemoveOk')}
                cancelText={t('selfEvolutionRun.artifactRemoveCancel')}
                okButtonProps={{ danger: true }}
                placement="left"
              >
                <Button
                  size="small"
                  type="text"
                  icon={<CloseOutlined />}
                  className="artifact-row-joined-remove"
                  title={t('selfEvolutionRun.artifactRemoveTooltip')}
                />
              </Popconfirm>
            </div>
          ) : (
            <Dropdown
              menu={{
                items: assignTargets.map((target) => {
                  const isDisabled =
                    target.isComposite &&
                    target.listConstraint !== undefined &&
                    target.listConstraint !== thisCardinality;
                  const disabledHint = isDisabled
                    ? thisCardinality === 'list'
                      ? t('selfEvolutionRun.artifactListConflictSingle')
                      : t('selfEvolutionRun.artifactListConflictList')
                    : undefined;
                  return {
                    key: target.key,
                    disabled: isDisabled,
                    label: disabledHint ? (
                      <Tooltip title={disabledHint} placement='right'>
                        <span style={{ color: '#bfbfbf', cursor: 'not-allowed', display: 'block' }}>
                          {target.label}
                        </span>
                      </Tooltip>
                    ) : target.label,
                    onClick: isDisabled ? undefined : () => onAssign(target, art.id, { widgetType: selectedWidget } as WidgetConfig),
                  };
                }),
              }}
              trigger={['click']}
            >
              <Button size="small" className="artifact-row-join">
                {t('selfEvolutionRun.artifactJoinMenuLabel')} <DownOutlined />
              </Button>
            </Dropdown>
          )}
        </div>
      )}
    </div>
  );
}

// ── Main component ───────────────────────────────────────────────────────────
export default function ArtifactPanel({ model, onClose, onModelChange, uiMode, inline, pluginModel, onUiModelChange, onTabNavigate, readonly = false }: Props) {
  const { t } = useTranslation();
  const [newDraft, setNewDraft] = useState<EditDraft>(EMPTY_DRAFT);
  const [adding, setAdding] = useState(false);

  const artifacts = Object.values(model.slots);
  const tabs: PluginUiTab[] = pluginModel?.ui?.tabs ?? [];
  const uiSlots: Record<string, WidgetConfig> = (pluginModel?.ui?.slots ?? {}) as Record<string, WidgetConfig>;
  const slotMap: Record<string, SlotDef> = model.slots;

  const validateId = (id: string): string | undefined => {
    if (!id.trim()) return t('selfEvolutionRun.artifactPanelIdErrorEmpty');
    if (!ARTIFACT_ID_REGEX.test(id)) return t('selfEvolutionRun.artifactPanelIdErrorInvalid');
    if (model.slots[id]) return t('selfEvolutionRun.artifactPanelIdErrorDuplicate');
    return undefined;
  };

  const handleAdd = () => {
    const idError = validateId(newDraft.id);
    if (idError) {
      setNewDraft((d) => ({ ...d, idError }));
      return;
    }
    const isList = newDraft.cardinality === 'list';
    const maxChars = parseInt(newDraft.summary_max_chars, 10);
    const newSlot: SlotDef = {
      id: newDraft.id,
      type: newDraft.type,
      label: newDraft.label || undefined,
      cardinality: isList ? 'list' : undefined,
      ordered: (isList && newDraft.ordered) ? true : undefined,
      allow_manual_add: isList ? newDraft.allow_manual_add : undefined,
      external: newDraft.external || undefined,
      summary_max_chars: (!isNaN(maxChars) && maxChars > 0) ? maxChars : undefined,
    };
    onModelChange((prev) => ({ ...prev, slots: { ...prev.slots, [newDraft.id]: newSlot } }));
    setNewDraft(EMPTY_DRAFT);
    setAdding(false);
  };

  const handleDelete = (id: string) => {
    onModelChange((prev) => {
      const newSlots = { ...prev.slots };
      delete newSlots[id];
      const newNodes = prev.nodes.map((n) => ({
        ...n,
        inputs: n.inputs
          .filter((input) => input.material !== id)
          .map((input) => ({
            ...input,
            alternatives: input.alternatives?.filter((material) => material !== id),
          })),
        outputs: n.outputs.filter((r) => r.material !== id),
        skipIf: removeMaterialFromExpression(n.skipIf, id),
        transitions: n.transitions.map((transition) => ({
          ...transition,
          condition: removeMaterialFromExpression(transition.condition, id),
        })),
      }));
      const startTransitions = prev.startTransitions.map((transition) => ({
        ...transition,
        condition: removeMaterialFromExpression(transition.condition, id),
      }));
      return { ...prev, slots: newSlots, nodes: newNodes, startTransitions };
    });
  };

  const updateArtifact = (id: string, patch: Partial<Omit<SlotDef, 'id'>>) => {
    onModelChange((prev) => {
      const current = prev.slots[id];
      const updated: SlotDef = { ...current, ...patch };
      if ('cardinality' in patch && patch.cardinality !== 'list') {
        updated.cardinality = undefined;
        updated.ordered = undefined;
        updated.allow_manual_add = undefined;
      }
      return { ...prev, slots: { ...prev.slots, [id]: updated } };
    });
  };

  const assignSlot = (target: AssignTarget, slotId: string, widget: WidgetConfig) => {
    if (!pluginModel || !onUiModelChange) return;
    const nextUiSlots = { ...(pluginModel.ui?.slots ?? {}), [slotId]: widget };

    if (!target.isComposite) {
      // Simple tab: add to tab.slots
      const newTabs = tabs.map((tab) =>
        tab.id === target.tabId && !tab.slots.some((s) => s.id === slotId)
          ? { ...tab, slots: [...tab.slots, { id: slotId }] }
          : tab,
      );
      onUiModelChange({ ...(pluginModel.ui ?? { tabs: [] }), tabs: newTabs, slots: nextUiSlots });
    } else {
      // Composite tab: assign slot into the tree
      const newTabs = tabs.map((tab) => {
        if (tab.id !== target.tabId || !tab.composite_layout) return tab;
        const newLayout = assignInComposite(tab.composite_layout, target.blockPath!, target.tabIdx, slotId);
        return { ...tab, composite_layout: newLayout };
      });
      onUiModelChange({ ...(pluginModel.ui ?? { tabs: [] }), tabs: newTabs, slots: nextUiSlots });
    }
  };

  const removeSlotFromUi = (slotId: string) => {
    if (!pluginModel || !onUiModelChange) return;
    // Remove from simple tabs
    const newTabs = tabs.map((tab) => {
      if (tab.layout !== 'composite') {
        return { ...tab, slots: tab.slots.filter((s) => s.id !== slotId) };
      }
      if (!tab.composite_layout) return tab;
      const newLayout = removeFromComposite(tab.composite_layout, slotId);
      return { ...tab, composite_layout: newLayout };
    });
    onUiModelChange({ ...(pluginModel.ui ?? { tabs: [] }), tabs: newTabs });
  };

  const updateWidget = (slotId: string, widget: WidgetConfig) => {
    if (!pluginModel || !onUiModelChange) return;
    const nextUiSlots = { ...(pluginModel.ui?.slots ?? {}), [slotId]: widget };
    onUiModelChange({ ...(pluginModel.ui ?? { tabs: [] }), tabs, slots: nextUiSlots });
  };

  return (
    <div
      className={`artifact-panel${inline ? ' artifact-panel--inline' : ''}`}
      role="complementary"
      aria-label={t('selfEvolutionRun.artifactPanelAria')}
      onDoubleClick={(e) => e.stopPropagation()}
    >
      <div className="artifact-panel-header">
        <span className="artifact-panel-title">{t('selfEvolutionRun.artifactPanelTitle')}</span>
        {!inline && (
          <Button type="text" icon={<CloseOutlined />} size="small" onClick={onClose} aria-label={t('selfEvolutionRun.artifactPanelClose')} />
        )}
      </div>

      <div className="artifact-panel-desc">
        {t('selfEvolutionRun.artifactPanelDesc')}
      </div>

      <div className="artifact-panel-body">
        {artifacts.length === 0 && !adding && (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={t('selfEvolutionRun.artifactPanelEmpty')}
            style={{ margin: '24px 0' }}
          />
        )}

        {artifacts.map((art) => (
          <ArtifactRow
            key={art.id}
            art={art}
            model={model}
            uiMode={uiMode}
            tabs={tabs}
            uiSlots={uiSlots}
            slotMap={slotMap}
            onUpdate={updateArtifact}
            onDelete={handleDelete}
            onAssign={assignSlot}
            onRemoveFromUi={removeSlotFromUi}
            onWidgetChange={updateWidget}
            onTabNavigate={onTabNavigate}
            readonly={readonly}
          />
        ))}

        {adding && (
          <div className="artifact-item artifact-item--new">
            <EditForm
              draft={newDraft}
              isNew={true}
              onChange={(patch) => setNewDraft((d) => ({ ...d, ...patch }))}
              onSave={handleAdd}
              onCancel={() => { setAdding(false); setNewDraft(EMPTY_DRAFT); }}
              saveLabel={t('selfEvolutionRun.artifactPanelConfirmAdd')}
            />
          </div>
        )}
      </div>

      {!adding && !readonly && (
        <div className="artifact-panel-footer">
          <Button
            type="dashed"
            size="small"
            icon={<PlusOutlined />}
            block
            onClick={() => setAdding(true)}
          >
            {t('selfEvolutionRun.artifactPanelAdd')}
          </Button>
        </div>
      )}
    </div>
  );
}
