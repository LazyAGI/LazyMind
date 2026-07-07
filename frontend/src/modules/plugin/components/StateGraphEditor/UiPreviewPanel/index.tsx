import { Tabs, Empty } from 'antd';
import { FileTextOutlined, PictureOutlined, FileOutlined, CodeOutlined } from '@ant-design/icons';
import type { PluginModel } from '../core/pluginModel';
import './index.scss';

interface Props {
  model: PluginModel;
}

const TYPE_ICONS: Record<string, React.ReactNode> = {
  text: <FileTextOutlined />,
  image: <PictureOutlined />,
  file: <FileOutlined />,
  json: <CodeOutlined />,
};

const TYPE_LABELS: Record<string, string> = {
  text: '文本',
  image: '图片',
  file: '文件',
  json: 'JSON',
};

export default function UiPreviewPanel({ model }: Props) {
  if (!model.ui?.tabs || model.ui.tabs.length === 0) {
    return (
      <div className="ui-preview-panel ui-preview-panel--empty">
        <Empty description="当前插件未配置 UI 布局" />
        <p className="upp-hint">在「插件配置」Tab 的 UI 布局区域中添加 Tab 和素材来配置 UI</p>
      </div>
    );
  }

  const slotMap = Object.fromEntries(model.slots.map((s) => [s.id, s]));

  return (
    <div className="ui-preview-panel">
      <p className="upp-readonly-note">只读预览 — 展示用户在插件运行时看到的 UI 布局</p>
      <Tabs
        items={model.ui.tabs.map((tab) => ({
          key: tab.id,
          label: tab.label ?? tab.id,
          children: (
            <div className={`upp-tab-content upp-layout-${tab.layout ?? 'list'}`}>
              {tab.slots.length === 0 ? (
                <p className="upp-hint">此 Tab 暂无素材</p>
              ) : (
                tab.slots.map((s) => {
                  const def = slotMap[s.id];
                  const typeIcon = def ? TYPE_ICONS[def.type] ?? <FileTextOutlined /> : <FileTextOutlined />;
                  const typeLabel = def ? TYPE_LABELS[def.type] ?? def.type : s.id;
                  return (
                    <div key={s.id} className="upp-slot-card">
                      <span className="upp-slot-icon">{typeIcon}</span>
                      <span className="upp-slot-label">{def?.label ?? s.id}</span>
                      <span className="upp-slot-type">{typeLabel}</span>
                      {def?.cardinality === 'list' && (
                        <span className="upp-slot-cardinality">列表</span>
                      )}
                    </div>
                  );
                })
              )}
            </div>
          ),
        }))}
      />
    </div>
  );
}
