import { useState } from 'react';
import { Button } from 'antd';
import { ExpandOutlined, CompressOutlined, CloseOutlined, FileTextOutlined } from '@ant-design/icons';
import type { PluginModel, PluginUiTab, WidgetConfig, CompositePanelNode, WidgetType } from '../core/pluginModel';
import { SLOT_DEFAULT_WIDGET } from '../core/pluginModel';
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
  pluginModel: PluginModel;
  onGraphModelChange: (updater: (prev: GraphModel) => GraphModel) => void;
  onPluginModelChange: (m: PluginModel) => void;
  activeTabId: string | undefined;
  onActiveTabChange: (tabId: string | undefined) => void;
}

export default function UiEditorPanel({
  graphModel,
  pluginModel,
  onGraphModelChange,
  onPluginModelChange,
  activeTabId,
  onActiveTabChange,
}: Props) {
  const [fullscreen, setFullscreen] = useState(false);
  const [selectedSlotId, setSelectedSlotId] = useState<string | null>(null);
  const [autoEditTabId, setAutoEditTabId] = useState<string | undefined>(undefined);
  const tabs: PluginUiTab[] = pluginModel.ui?.tabs ?? [];
  const activeTab = tabs.find((t) => t.id === activeTabId);
  const slotMap = Object.fromEntries(Object.values(graphModel.slots).map((s) => [s.id, s]));
  const uiSlots: Record<string, WidgetConfig> = (pluginModel.ui?.slots ?? {}) as Record<string, WidgetConfig>;

  const updateTabs = (newTabs: PluginUiTab[]) => {
    onPluginModelChange({
      ...pluginModel,
      ui: { ...(pluginModel.ui ?? { tabs: [] }), tabs: newTabs },
    });
  };

  const handleUiChange = (ui: PluginModel['ui']) => {
    onPluginModelChange({ ...pluginModel, ui });
  };

  const handleAddTab = () => {
    const id = nextTabId();
    const newTab: PluginUiTab = { id, label: '新 Tab', layout: 'vertical', slots: [] };
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
    if (activeTabId === tabId) onActiveTabChange(newTabs[0]?.id);
  };

  const handleSlotsChange = (slots: Array<{ id: string }>) => {
    if (!activeTabId) return;
    updateTabs(tabs.map((t) => t.id === activeTabId ? { ...t, slots } : t));
  };

  const handleUiSlotsChange = (slotId: string, widget: WidgetConfig | undefined) => {
    const currentUiSlots = pluginModel.ui?.slots ?? {};
    const nextSlots = { ...currentUiSlots };
    if (widget === undefined) {
      delete nextSlots[slotId];
    } else {
      nextSlots[slotId] = widget;
    }
    onPluginModelChange({
      ...pluginModel,
      ui: { ...(pluginModel.ui ?? { tabs: [] }), slots: nextSlots },
    });
  };

  const handleCompositeLayoutChange = (value: CompositePanelNode) => {
    if (!activeTabId) return;
    updateTabs(tabs.map((t) => t.id === activeTabId ? { ...t, composite_layout: value } : t));
  };

  const handleCompositeTabPositionChange = (pos: PluginUiTab['composite_tab_position']) => {
    if (!activeTabId) return;
    updateTabs(tabs.map((t) => t.id === activeTabId ? { ...t, composite_tab_position: pos } : t));
  };

  const handleLayoutChange = (layout: PluginUiTab['layout']) => {
    if (!activeTabId) return;
    updateTabs(tabs.map((t) => (t.id === activeTabId ? { ...t, layout } : t)));
  };

  const handleGridColsChange = (gridCols: number | null) => {
    if (!activeTabId) return;
    updateTabs(tabs.map((t) => t.id === activeTabId ? { ...t, gridCols: gridCols ?? undefined } : t));
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
            pluginModel={pluginModel}
            activeTabId={activeTabId}
            onUiModelChange={handleUiChange}
            onTabNavigate={onActiveTabChange}
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
            if (slotId && activeTabId) {
              const currentTab = tabs.find((t) => t.id === activeTabId);
              if (!currentTab || currentTab.slots.some((s) => s.id === slotId)) return;
              handleSlotsChange([...(currentTab.slots ?? []), { id: slotId }]);
            }
          }}
        >
          <UiWysiwygPreview
            pluginModel={pluginModel}
            activeTabId={activeTabId}
            activeLayout={activeTab?.layout ?? 'vertical'}
            activeGridCols={activeTab?.gridCols}
            slotMap={slotMap}
            selectedSlotId={selectedSlotId}
            onSelectSlot={setSelectedSlotId}
            autoEditTabId={autoEditTabId}
            onAutoEditDone={() => setAutoEditTabId(undefined)}
            onTabSelect={onActiveTabChange}
            onAddTab={handleAddTab}
            onRenameTab={handleRenameTab}
            onDeleteTab={handleDeleteTab}
            onLayoutChange={handleLayoutChange}
            onGridColsChange={handleGridColsChange}
            onSlotsChange={handleSlotsChange}
            onCompositeLayoutChange={handleCompositeLayoutChange}
            onCompositeTabPositionChange={handleCompositeTabPositionChange}
            editorMode
            extraRightAction={
              <Button
                type="text"
                size="small"
                icon={fullscreen ? <CompressOutlined /> : <ExpandOutlined />}
                className="uep-expand-btn"
                onClick={() => setFullscreen((v) => !v)}
                title={fullscreen ? '退出全屏' : '全屏预览'}
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
                <span className="uep-props-panel-subtitle">材料属性编辑</span>
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
