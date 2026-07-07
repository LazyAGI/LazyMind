import { useState } from 'react';
import { Button, Dropdown } from 'antd';
import { AppstoreOutlined } from '@ant-design/icons';
import type { PluginModel, PluginUiTab } from '../core/pluginModel';
import type { GraphModel } from '../core/model';
import ArtifactPanel from '../ArtifactPanel';
import TabBar from './TabBar';
import UiEditorCanvas from './UiEditorCanvas';
import './index.scss';

const LAYOUT_LABELS: Record<string, string> = {
  list: 'List',
  grid: 'Grid',
  horizontal: 'Horizontal',
  composite: 'Composite',
};

let tabSeq = 0;
function nextTabId() {
  tabSeq += 1;
  return `tab_${tabSeq}`;
}

interface Props {
  graphModel: GraphModel;
  pluginModel: PluginModel;
  onGraphModelChange: (m: GraphModel) => void;
  onPluginModelChange: (m: PluginModel) => void;
}

export default function UiEditorPanel({
  graphModel,
  pluginModel,
  onGraphModelChange,
  onPluginModelChange,
}: Props) {
  const tabs: PluginUiTab[] = pluginModel.ui?.tabs ?? [];
  const [activeTabId, setActiveTabId] = useState<string | undefined>(tabs[0]?.id);
  const [showArtifacts, setShowArtifacts] = useState(false);

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
    const newTabs = [...tabs, newTab];
    updateTabs(newTabs);
    setActiveTabId(id);
  };

  const handleRenameTab = (tabId: string, label: string) => {
    updateTabs(tabs.map((t) => (t.id === tabId ? { ...t, label } : t)));
  };

  const handleDeleteTab = (tabId: string) => {
    const newTabs = tabs.filter((t) => t.id !== tabId);
    updateTabs(newTabs);
    if (activeTabId === tabId) setActiveTabId(newTabs[0]?.id);
  };

  const handleSlotsChange = (slotIds: string[]) => {
    if (!activeTabId) return;
    updateTabs(
      tabs.map((t) =>
        t.id === activeTabId ? { ...t, slots: slotIds.map((id) => ({ id })) } : t,
      ),
    );
  };

  const handleCompositeLayoutChange = (value: string) => {
    if (!activeTabId) return;
    updateTabs(
      tabs.map((t) => (t.id === activeTabId ? { ...t, composite_layout: value } : t)),
    );
  };

  const handleLayoutChange = (layout: PluginUiTab['layout']) => {
    if (!activeTabId) return;
    updateTabs(tabs.map((t) => (t.id === activeTabId ? { ...t, layout } : t)));
  };

  const layoutMenuItems = (['list', 'grid', 'horizontal', 'composite'] as const).map((l) => ({
    key: l,
    label: LAYOUT_LABELS[l],
    onClick: () => handleLayoutChange(l),
  }));

  return (
    <div className="uep-root">
      {/* Toolbar */}
      <div className="uep-toolbar">
        <Button
          size="small"
          icon={<AppstoreOutlined />}
          type={showArtifacts ? 'primary' : 'default'}
          onClick={() => setShowArtifacts((v) => !v)}
        >
          素材
        </Button>
        {activeTab && (
          <Dropdown menu={{ items: layoutMenuItems }} trigger={['click']}>
            <Button size="small">
              布局: {LAYOUT_LABELS[activeTab.layout ?? 'list']} ▾
            </Button>
          </Dropdown>
        )}
      </div>

      {/* TabBar */}
      <TabBar
        tabs={tabs}
        activeTabId={activeTabId}
        onSelect={setActiveTabId}
        onAdd={handleAddTab}
        onRename={handleRenameTab}
        onDelete={handleDeleteTab}
      />

      {/* Body */}
      <div className="uep-body">
        {activeTab ? (
          <UiEditorCanvas
            tab={activeTab}
            slotMap={slotMap}
            onSlotsChange={handleSlotsChange}
            onCompositeLayoutChange={handleCompositeLayoutChange}
          />
        ) : (
          <div className="uep-no-tab">
            点击「新建 Tab」开始配置 UI 布局
          </div>
        )}

        {showArtifacts && (
          <ArtifactPanel
            model={graphModel}
            onClose={() => setShowArtifacts(false)}
            onModelChange={onGraphModelChange}
            uiMode
            pluginModel={pluginModel}
            activeTabId={activeTabId}
            onUiModelChange={handleUiChange}
          />
        )}
      </div>
    </div>
  );
}
