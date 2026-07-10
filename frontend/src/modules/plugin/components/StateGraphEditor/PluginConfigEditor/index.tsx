import { Form, Input } from 'antd';
import { useTranslation } from 'react-i18next';
import type { PluginModel } from '../core/pluginModel';
import './index.scss';

interface Props {
  model: PluginModel;
  onChange: (model: PluginModel) => void;
}

export default function PluginConfigEditor({ model, onChange }: Props) {
  const { t } = useTranslation();
  const update = (patch: Partial<PluginModel>) => onChange({ ...model, ...patch });

  return (
    <div className="plugin-config-editor">
      <section className="pce-section">
        <p className="pce-section-title">{t('selfEvolutionRun.pluginConfigEditorBasicInfo')}</p>
        <Form layout="vertical" size="small">
          <Form.Item label={t('selfEvolutionRun.pluginConfigEditorPluginId')}>
            <Input
              value={model.id}
              onChange={(e) => update({ id: e.target.value })}
              placeholder={t('selfEvolutionRun.pluginConfigEditorPluginIdPlaceholder')}
            />
          </Form.Item>
          <Form.Item label={t('selfEvolutionRun.pluginConfigEditorDisplayName')}>
            <Input
              value={model.name}
              onChange={(e) => update({ name: e.target.value })}
              placeholder={t('selfEvolutionRun.pluginInfoExamplePlaceholder')}
            />
          </Form.Item>
          <Form.Item label={t('selfEvolutionRun.pluginInfoFieldDescription')}>
            <Input.TextArea
              value={model.description ?? ''}
              onChange={(e) => update({ description: e.target.value })}
              placeholder={t('selfEvolutionRun.pluginInfoFieldDescriptionPlaceholder')}
              autoSize={{ minRows: 2, maxRows: 4 }}
            />
          </Form.Item>
          <Form.Item label={t('selfEvolutionRun.pluginConfigEditorWhenToUse')}>
            <Input.TextArea
              value={model.when_to_use ?? ''}
              onChange={(e) => update({ when_to_use: e.target.value })}
              placeholder={t('selfEvolutionRun.pluginInfoFieldWhenToUsePlaceholder')}
              autoSize={{ minRows: 2, maxRows: 4 }}
            />
          </Form.Item>
        </Form>
      </section>
    </div>
  );
}
