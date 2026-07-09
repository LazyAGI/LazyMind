import { Tabs, Empty } from 'antd';
import { FileTextOutlined, PictureOutlined, FileOutlined, CodeOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
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

export default function UiPreviewPanel({ model }: Props) {
  const { t } = useTranslation();

  const TYPE_LABELS: Record<string, string> = {
    text: t('selfEvolutionRun.uiPreviewTypeText'),
    image: t('selfEvolutionRun.uiPreviewTypeImage'),
    file: t('selfEvolutionRun.uiPreviewTypeFile'),
    json: 'JSON',
  };
  if (!model.ui?.tabs || model.ui.tabs.length === 0) {
    return (
      <div className="ui-preview-panel ui-preview-panel--empty">
        <Empty description={t('selfEvolutionRun.uiPreviewNoLayout')} />
        <p className="upp-hint">{t('selfEvolutionRun.uiPreviewHint')}</p>
      </div>
    );
  }

  const slotMap = Object.fromEntries(model.slots.map((s) => [s.id, s]));

  return (
    <div className="ui-preview-panel">
      <p className="upp-readonly-note">{t('selfEvolutionRun.uiPreviewReadonlyNote')}</p>
      <Tabs
        items={model.ui.tabs.map((tab) => ({
          key: tab.id,
          label: tab.label ?? tab.id,
          children: (
            <div className={`upp-tab-content upp-layout-${tab.layout ?? 'list'}`}>
              {tab.slots.length === 0 ? (
                <p className="upp-hint">{t('selfEvolutionRun.uiPreviewNoSlots')}</p>
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
                        <span className="upp-slot-cardinality">{t('selfEvolutionRun.uiPreviewSlotList')}</span>
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
