import type { PluginModel, PluginUiTab, WidgetConfig, CompositePanelNode } from '../core/pluginModel';
import type { GraphModel } from '../core/model';
import ArtifactPanel from '../ArtifactPanel';
import UiWysiwygPreview from './UiWysiwygPreview';
import './index.scss';

function nextTabId() {
  return `tab_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
}

interface Props {
  graphModel: GraphModel;
  pluginModel: PluginModel;
  onGraphModelChange: (m: GraphModel) => void;
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
  const tabs: PluginUiTab[] = pluginModel.ui?.tabs ?? [];
  const activeTab = tabs.find((t) => t.id === activeTabId);
  const slotMap = Object.fromEntries(Object.values(graphModel.slots).map((s) => [s.id, s]));

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
    const newTab: PluginUiTab = { id, label: `Tab ${tabs.length + 1}`, layout: 'vertical', slots: [] };
    updateTabs([...tabs, newTab]);
    onActiveTabChange(id);
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

  return (
    <div className="uep-root">
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
            onTabSelect={onActiveTabChange}
            onAddTab={handleAddTab}
            onRenameTab={handleRenameTab}
            onDeleteTab={handleDeleteTab}
            onLayoutChange={handleLayoutChange}
            onGridColsChange={handleGridColsChange}
            onSlotsChange={handleSlotsChange}
            onCompositeLayoutChange={handleCompositeLayoutChange}
            onCompositeTabPositionChange={handleCompositeTabPositionChange}
            onUiSlotsChange={handleUiSlotsChange}
          />
        </div>
      </div>
    </div>
  );
}
