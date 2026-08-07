import { Form, Input } from 'antd';
import { useTranslation } from 'react-i18next';
import type { WorkflowModel } from '../core/workflowModel';
import './index.scss';

interface Props {
  model: WorkflowModel;
  onChange: (model: WorkflowModel) => void;
}

export default function WorkflowConfigEditor({ model, onChange }: Props) {
  const { t } = useTranslation();
  const update = (patch: Partial<WorkflowModel>) => onChange({ ...model, ...patch });

  return (
    <div className="workflow-config-editor">
      <section className="pce-section">
        <p className="pce-section-title">{t('selfEvolutionRun.workflowConfigEditorBasicInfo')}</p>
        <Form layout="vertical" size="small">
          <Form.Item label={t('selfEvolutionRun.workflowConfigEditorWorkflowId')}>
            <Input
              value={model.id}
              onChange={(e) => update({ id: e.target.value })}
              placeholder={t('selfEvolutionRun.workflowConfigEditorWorkflowIdPlaceholder')}
            />
          </Form.Item>
          <Form.Item label={t('selfEvolutionRun.workflowConfigEditorDisplayName')}>
            <Input
              value={model.name}
              onChange={(e) => update({ name: e.target.value })}
              placeholder={t('selfEvolutionRun.workflowInfoExamplePlaceholder')}
            />
          </Form.Item>
          <Form.Item label={t('selfEvolutionRun.workflowInfoFieldDescription')}>
            <Input.TextArea
              value={model.description ?? ''}
              onChange={(e) => update({ description: e.target.value })}
              placeholder={t('selfEvolutionRun.workflowInfoFieldDescriptionPlaceholder')}
              autoSize={{ minRows: 2, maxRows: 4 }}
            />
          </Form.Item>
          <Form.Item label={t('selfEvolutionRun.workflowConfigEditorWhenToUse')}>
            <Input.TextArea
              value={model.when_to_use ?? ''}
              onChange={(e) => update({ when_to_use: e.target.value })}
              placeholder={t('selfEvolutionRun.workflowInfoFieldWhenToUsePlaceholder')}
              autoSize={{ minRows: 2, maxRows: 4 }}
            />
          </Form.Item>
        </Form>
      </section>
    </div>
  );
}
