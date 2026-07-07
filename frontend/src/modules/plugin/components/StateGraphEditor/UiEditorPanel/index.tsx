import type { PluginModel, PluginUiTab } from '../core/pluginModel';
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
    const newTab: PluginUiTab = { id, label: `Tab ${tabs.length + 1}`, layout: 'list', slots: [] };
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

  const handleAddSlotToTab = (slotId: string) => {
    if (!activeTabId) return;
    updateTabs(
      tabs.map((t) => {
        if (t.id !== activeTabId) return t;
        if (t.slots.some((s) => s.id === slotId)) return t;
        return { ...t, slots: [...t.slots, { id: slotId }] };
      }),
    );
  };

  const handleLayoutChange = (layout: PluginUiTab['layout']) => {
    if (!activeTabId) return;
    updateTabs(tabs.map((t) => (t.id === activeTabId ? { ...t, layout } : t)));
  };

  return (
    <div className="uep-root">
      {/* Body: fixed left ArtifactPanel + right WYSIWYG (no separate toolbar/tabbar rows) */}
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
            if (slotId) handleAddSlotToTab(slotId);
          }}
        >
          <UiWysiwygPreview
            pluginModel={pluginModel}
            activeTabId={activeTabId}
            activeLayout={activeTab?.layout ?? 'list'}
            slotMap={slotMap}
            onTabSelect={onActiveTabChange}
            onAddTab={handleAddTab}
            onRenameTab={handleRenameTab}
            onDeleteTab={handleDeleteTab}
            onLayoutChange={handleLayoutChange}
          />
        </div>
      </div>
    </div>
  );
}
