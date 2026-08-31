import { useEffect, useState } from 'react';
import { Button } from 'antd';
import { ExpandOutlined, CompressOutlined, CloseOutlined, FileTextOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { WorkflowModel, WorkflowUiTab, WidgetConfig, CompositePanelNode, WidgetType } from '../core/workflowModel';
import { collectCompositeSlotIds, SLOT_DEFAULT_WIDGET } from '../core/workflowModel';
import type { GraphModel } from '../core/model';
import ArtifactPanel from '../ArtifactPanel';
import UiWysiwygPreview from './UiWysiwygPreview';
import WidgetSelector from './WidgetSelector';
import WidgetConfigPanel from './WidgetConfigPanel';
import { SLOT_TYPE_ICONS } from './slotTypeIcon';
import './index.scss';

function nextTabId() {
  return `tab_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
}

interface Props {
  graphModel: GraphModel;
  workflowModel: WorkflowModel;
  onGraphModelChange: (updater: (prev: GraphModel) => GraphModel) => void;
  onWorkflowModelChange: (m: WorkflowModel) => void;
  activeTabId: string | undefined;
  onActiveTabChange: (tabId: string | undefined) => void;
  readonly?: boolean;
}

export default function UiEditorPanel({
  graphModel,
  workflowModel,
  onGraphModelChange,
  onWorkflowModelChange,
  activeTabId,
  onActiveTabChange,
  readonly = false,
}: Props) {
  const { t } = useTranslation();
  const [fullscreen, setFullscreen] = useState(false);
  const [selectedSlotId, setSelectedSlotId] = useState<string | null>(null);
  const [autoEditTabId, setAutoEditTabId] = useState<string | undefined>(undefined);
  const tabs: WorkflowUiTab[] = workflowModel.ui?.tabs ?? [];
  const effectiveActiveTabId = tabs.some((t) => t.id === activeTabId)
    ? activeTabId
    : tabs[0]?.id;
  const activeTab = tabs.find((t) => t.id === effectiveActiveTabId);
  const slotMap = Object.fromEntries(Object.values(graphModel.slots).map((s) => [s.id, s]));
  const uiSlots: Record<string, WidgetConfig> = (workflowModel.ui?.slots ?? {}) as Record<string, WidgetConfig>;

  // The UI editor may mount before a tab has been selected. Its preview already
  // falls back to the first persisted tab, so keep the lifted selection in sync
  // with that same tab instead of letting controls fall back to Vertical.
  useEffect(() => {
    if (effectiveActiveTabId !== activeTabId) {
      onActiveTabChange(effectiveActiveTabId);
    }
  }, [activeTabId, effectiveActiveTabId, onActiveTabChange]);

  const updateTabs = (newTabs: WorkflowUiTab[]) => {
    onWorkflowModelChange({
      ...workflowModel,
      ui: { ...(workflowModel.ui ?? { tabs: [] }), tabs: newTabs },
    });
  };

  const handleUiChange = (ui: WorkflowModel['ui']) => {
    onWorkflowModelChange({ ...workflowModel, ui });
  };

  const handleAddTab = () => {
    const id = nextTabId();
    const newTab: WorkflowUiTab = { id, label: t('selfEvolutionRun.uiEditorNewTabLabel'), layout: 'vertical', slots: [] };
    updateTabs([...tabs, newTab]);
    onActiveTabChange(id);
    setAutoEditTabId(id);
  };

  const handleRenameTab = (tabId: string, label: string) => {
    updateTabs(tabs.map((t) => (t.id === tabId ? { ...t, label } : t)));
  };

  const handleDeleteTab = (tabId: string) => {
    const newTabs = tabs.filter((t) => t.id !== tabId);
    updateTabs(newTabs);
    if (effectiveActiveTabId === tabId) onActiveTabChange(newTabs[0]?.id);
  };

  const handleSlotsChange = (slots: Array<{ id: string }>) => {
    if (!effectiveActiveTabId) return;
    updateTabs(tabs.map((t) => t.id === effectiveActiveTabId ? { ...t, slots } : t));
  };

  const handleUiSlotsChange = (slotId: string, widget: WidgetConfig | undefined) => {
    const currentUiSlots = workflowModel.ui?.slots ?? {};
    const nextSlots = { ...currentUiSlots };
    if (widget === undefined) {
      delete nextSlots[slotId];
    } else {
      nextSlots[slotId] = widget;
    }
    onWorkflowModelChange({
      ...workflowModel,
      ui: { ...(workflowModel.ui ?? { tabs: [] }), slots: nextSlots },
    });
  };

  const handleCompositeLayoutChange = (value: CompositePanelNode) => {
    if (!effectiveActiveTabId) return;
    const layoutSlots = collectCompositeSlotIds(value).map((id) => ({ id }));
    updateTabs(tabs.map((t) => t.id === effectiveActiveTabId
      ? { ...t, slots: layoutSlots, composite_layout: value }
      : t));
  };

  const handleCompositeTabPositionChange = (pos: WorkflowUiTab['composite_tab_position']) => {
    if (!effectiveActiveTabId) return;
    updateTabs(tabs.map((t) => t.id === effectiveActiveTabId ? { ...t, composite_tab_position: pos } : t));
  };

  const handleLayoutChange = (layout: WorkflowUiTab['layout']) => {
    if (!effectiveActiveTabId) return;
    updateTabs(tabs.map((t) => (t.id === effectiveActiveTabId ? { ...t, layout } : t)));
  };

  const handleGridColsChange = (gridCols: number | null) => {
    if (!effectiveActiveTabId) return;
    updateTabs(tabs.map((t) => t.id === effectiveActiveTabId ? { ...t, gridCols: gridCols ?? undefined } : t));
  };

  // Selected slot info for the properties panel
  const selectedSlotDef = selectedSlotId ? slotMap[selectedSlotId] : undefined;
  const selectedType = selectedSlotDef?.type ?? 'text';
  const selectedCardinality = selectedSlotDef?.cardinality;
  const selectedSlotKey = `${selectedType}/${selectedCardinality ?? 'single'}`;
  const selectedDefaultWidget = (SLOT_DEFAULT_WIDGET[selectedSlotKey] ?? 'text-single') as WidgetType;
  const selectedWidget: WidgetConfig = (selectedSlotId ? uiSlots[selectedSlotId] : undefined) ?? ({ widgetType: selectedDefaultWidget } as WidgetConfig);
  const selectedLabel = selectedSlotDef?.label ?? selectedSlotId ?? '';
  const selectedIcon = SLOT_TYPE_ICONS[selectedType] ?? <FileTextOutlined />;

  return (
    <div className={`uep-root${fullscreen ? ' uep-root--fullscreen' : ''}`}>
      <div className="uep-body">
        <div className="uep-sidebar">
          <ArtifactPanel
            model={graphModel}
            onClose={() => {}}
            onModelChange={onGraphModelChange}
            uiMode
            inline
            workflowModel={workflowModel}
            activeTabId={effectiveActiveTabId}
            onUiModelChange={handleUiChange}
            onTabNavigate={onActiveTabChange}
            readonly={readonly}
          />
        </div>

        <div
          className="uep-canvas-area"
          onDragOver={(e) => {
            if (e.dataTransfer.types.includes('application/x-slot-id')) {
              e.preventDefault();
              e.dataTransfer.dropEffect = 'copy';
            }
          }}
          onDrop={(e) => {
            e.preventDefault();
            const slotId = e.dataTransfer.getData('application/x-slot-id');
            if (slotId && effectiveActiveTabId) {
              const currentTab = tabs.find((t) => t.id === effectiveActiveTabId);
              if (!currentTab || currentTab.slots.some((s) => s.id === slotId)) return;
              handleSlotsChange([...(currentTab.slots ?? []), { id: slotId }]);
            }
          }}
        >
          <UiWysiwygPreview
            workflowModel={workflowModel}
            activeTabId={effectiveActiveTabId}
            activeLayout={activeTab?.layout ?? 'vertical'}
            activeGridCols={activeTab?.gridCols}
            slotMap={slotMap}
            selectedSlotId={selectedSlotId}
            onSelectSlot={readonly ? () => {} : setSelectedSlotId}
            autoEditTabId={autoEditTabId}
            onAutoEditDone={() => setAutoEditTabId(undefined)}
            onTabSelect={onActiveTabChange}
            onAddTab={readonly ? () => {} : handleAddTab}
            onRenameTab={readonly ? () => {} : handleRenameTab}
            onDeleteTab={readonly ? () => {} : handleDeleteTab}
            onLayoutChange={readonly ? () => {} : handleLayoutChange}
            onGridColsChange={readonly ? () => {} : handleGridColsChange}
            onSlotsChange={readonly ? () => {} : handleSlotsChange}
            onCompositeLayoutChange={readonly ? () => {} : handleCompositeLayoutChange}
            onCompositeTabPositionChange={readonly ? () => {} : handleCompositeTabPositionChange}
            editorMode={!readonly}
            extraRightAction={
              <Button
                type="text"
                size="small"
                icon={fullscreen ? <CompressOutlined /> : <ExpandOutlined />}
                className="uep-expand-btn"
                onClick={() => setFullscreen((v) => !v)}
                title={fullscreen ? t('selfEvolutionRun.uiEditorExitFullscreen') : t('selfEvolutionRun.uiEditorEnterFullscreen')}
              />
            }
          />
        </div>

        {selectedSlotId && (
          <div className="uep-props-panel">
            <div className="uep-props-panel-header">
              <span className="uep-props-panel-icon">{selectedIcon}</span>
              <div className="uep-props-panel-header-text">
                <span className="uep-props-panel-title">{selectedLabel}</span>
            <span className="uep-props-panel-subtitle">{t('selfEvolutionRun.uiEditorPropsSubtitle')}</span>
              </div>
              <Button
                type="text"
                size="small"
                icon={<CloseOutlined />}
                onClick={() => setSelectedSlotId(null)}
                className="uep-props-panel-close"
              />
            </div>
            <div className="uep-props-panel-body">
              <div className="uep-props-panel-widget-type">
                <WidgetSelector
                  slotType={selectedType}
                  cardinality={selectedCardinality}
                  value={selectedWidget.widgetType}
                  onChange={(newType) => handleUiSlotsChange(selectedSlotId, { widgetType: newType } as WidgetConfig)}
                  size="small"
                />
              </div>
              <WidgetConfigPanel
                config={selectedWidget}
                onChange={(next) => handleUiSlotsChange(selectedSlotId, next)}
              />
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
