import { useState } from 'react';
import { Button, Checkbox, Input, InputNumber, Select, Tooltip, Empty, Dropdown, Popconfirm } from 'antd';
import { PlusOutlined, CloseOutlined, CheckOutlined, DownOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { SlotDef, GraphModel } from '../core/model';
import type { PluginModel, PluginUiTab, WidgetConfig, WidgetType } from '../core/pluginModel';
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
  onModelChange: (model: GraphModel) => void;
  uiMode?: boolean;
  /** When true, renders as an inline block (position: static) instead of the default floating overlay */
  inline?: boolean;
  pluginModel?: PluginModel;
  activeTabId?: string;
  onUiModelChange?: (ui: PluginModel['ui']) => void;
}

interface EditDraft {
  id: string;
  label: string;
  type: string;
  cardinality: 'single' | 'list';
  ordered: boolean;
  allow_manual_add: boolean;
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
  summary_max_chars: '',
};

/** Returns true if any step node uses slotId as an input. */
function isUsedAsInput(model: GraphModel, slotId: string): boolean {
  return model.nodes.some((n) => n.inputs.some((r) => r.slot === slotId));
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
  onUpdate: (id: string, patch: Partial<Omit<SlotDef, 'id'>>) => void;
  onDelete: (id: string) => void;
  onJoinTab: (slotId: string, tabId: string, widget: WidgetConfig) => void;
  onLeaveTab: (slotId: string, tabId: string) => void;
  onWidgetChange: (slotId: string, tabId: string, widget: WidgetConfig) => void;
}

function ArtifactRow({ art, model, uiMode, tabs, onUpdate, onDelete, onJoinTab, onLeaveTab, onWidgetChange }: ArtifactRowProps) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<EditDraft>(EMPTY_DRAFT);

  // Selected widget type for this slot when joining a tab
  const slotKey = `${art.type}/${art.cardinality ?? 'single'}`;
  const defaultWidgetType: WidgetType = (SLOT_DEFAULT_WIDGET[slotKey] ?? 'text-single') as WidgetType;
  const [selectedWidget, setSelectedWidget] = useState<WidgetType>(defaultWidgetType);

  const resolveAllowManualAdd = (): boolean => {
    if (art.allow_manual_add !== undefined) return art.allow_manual_add;
    return isUsedAsInput(model, art.id);
  };

  // Find which tab (if any) this slot currently belongs to — at most one tab.
  const currentTab = tabs.find((tab) => tab.slots.some((s) => s.id === art.id)) ?? null;
  const currentTabSlot = currentTab?.slots.find((s) => s.id === art.id);

  const startEdit = () => {
    setDraft({
      id: art.id,
      label: art.label ?? '',
      type: art.type,
      cardinality: art.cardinality === 'list' ? 'list' : 'single',
      ordered: !!art.ordered,
      allow_manual_add: resolveAllowManualAdd(),
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
          <Button size="small" type="text" className="artifact-item-edit-btn" onClick={startEdit}>
            {t('selfEvolutionRun.artifactPanelEdit')}
          </Button>
          {!uiMode && (
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
          {/* Widget selector — only show compatible types */}
          {compatibleWidgets.length > 1 && !currentTab && (
            <WidgetSelector
              slotType={art.type}
              cardinality={art.cardinality}
              value={selectedWidget}
              onChange={(wt) => setSelectedWidget(wt)}
              size="small"
            />
          )}
          {/* Widget selector for already-joined tab slot */}
          {compatibleWidgets.length > 1 && currentTab && currentTabSlot && (
            <WidgetSelector
              slotType={art.type}
              cardinality={art.cardinality}
              value={currentTabSlot.widget?.widgetType as WidgetType | undefined}
              onChange={(wt) => {
                const newWidget: WidgetConfig = { widgetType: wt } as WidgetConfig;
                onWidgetChange(art.id, currentTab.id, newWidget);
              }}
              size="small"
            />
          )}
          {currentTab ? (
            <Button
              size="small"
              type="link"
              className="artifact-row-join artifact-row-join--active"
              icon={<CheckOutlined />}
              onClick={() => onLeaveTab(art.id, currentTab.id)}
            >
              {t('selfEvolutionRun.artifactPanelJoinedTab', { tabLabel: currentTab.label ?? currentTab.id })}
            </Button>
          ) : (
            <Dropdown
              menu={{
                items: tabs.map((tab) => ({
                  key: tab.id,
                  label: tab.label ?? tab.id,
                  onClick: () => onJoinTab(art.id, tab.id, { widgetType: selectedWidget } as WidgetConfig),
                })),
              }}
              trigger={['click']}
            >
              <Button size="small" className="artifact-row-join">
                {t('selfEvolutionRun.artifactPanelJoinTab')} <DownOutlined />
              </Button>
            </Dropdown>
          )}
        </div>
      )}
    </div>
  );
}

// ── Main component ───────────────────────────────────────────────────────────
export default function ArtifactPanel({ model, onClose, onModelChange, uiMode, inline, pluginModel, onUiModelChange }: Props) {
  const { t } = useTranslation();
  const [newDraft, setNewDraft] = useState<EditDraft>(EMPTY_DRAFT);
  const [adding, setAdding] = useState(false);

  const artifacts = Object.values(model.slots);
  const tabs: PluginUiTab[] = pluginModel?.ui?.tabs ?? [];

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
      summary_max_chars: (!isNaN(maxChars) && maxChars > 0) ? maxChars : undefined,
    };
    onModelChange({ ...model, slots: { ...model.slots, [newDraft.id]: newSlot } });
    setNewDraft(EMPTY_DRAFT);
    setAdding(false);
  };

  const handleDelete = (id: string) => {
    const newSlots = { ...model.slots };
    delete newSlots[id];
    const newNodes = model.nodes.map((n) => ({
      ...n,
      inputs: n.inputs.filter((r) => r.slot !== id),
      outputs: n.outputs.filter((r) => r.slot !== id),
    }));
    onModelChange({ ...model, slots: newSlots, nodes: newNodes });
  };

  const updateArtifact = (id: string, patch: Partial<Omit<SlotDef, 'id'>>) => {
    const current = model.slots[id];
    const updated: SlotDef = { ...current, ...patch };
    if ('cardinality' in patch && patch.cardinality !== 'list') {
      updated.cardinality = undefined;
      updated.ordered = undefined;
      updated.allow_manual_add = undefined;
    }
    onModelChange({ ...model, slots: { ...model.slots, [id]: updated } });
  };

  const joinTab = (slotId: string, tabId: string, widget: WidgetConfig) => {
    if (!pluginModel || !onUiModelChange) return;
    const newTabs = tabs.map((tab) =>
      tab.id === tabId && !tab.slots.some((s) => s.id === slotId)
        ? { ...tab, slots: [...tab.slots, { id: slotId, widget }] }
        : tab,
    );
    onUiModelChange({ ...(pluginModel.ui ?? { tabs: [] }), tabs: newTabs });
  };

  const leaveTab = (slotId: string, tabId: string) => {
    if (!pluginModel || !onUiModelChange) return;
    const newTabs = tabs.map((tab) =>
      tab.id === tabId ? { ...tab, slots: tab.slots.filter((s) => s.id !== slotId) } : tab,
    );
    onUiModelChange({ ...(pluginModel.ui ?? { tabs: [] }), tabs: newTabs });
  };

  const updateWidget = (slotId: string, tabId: string, widget: WidgetConfig) => {
    if (!pluginModel || !onUiModelChange) return;
    const newTabs = tabs.map((tab) =>
      tab.id === tabId
        ? { ...tab, slots: tab.slots.map((s) => s.id === slotId ? { ...s, widget } : s) }
        : tab,
    );
    onUiModelChange({ ...(pluginModel.ui ?? { tabs: [] }), tabs: newTabs });
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
            onUpdate={updateArtifact}
            onDelete={handleDelete}
            onJoinTab={joinTab}
            onLeaveTab={leaveTab}
            onWidgetChange={updateWidget}
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

      {!adding && (
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
