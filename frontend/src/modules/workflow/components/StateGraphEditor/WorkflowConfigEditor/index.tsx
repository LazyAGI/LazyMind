import { Form, Input, Select } from 'antd';
import { useTranslation } from 'react-i18next';
import type { WorkflowModel, WorkflowRuntimePolicy } from '../core/workflowModel';
import './index.scss';

interface Props {
  model: WorkflowModel;
  onChange: (model: WorkflowModel) => void;
}

export default function WorkflowConfigEditor({ model, onChange }: Props) {
  const { t } = useTranslation();
  const update = (patch: Partial<WorkflowModel>) => onChange({ ...model, ...patch });
  const clarificationFields = model.runtime?.clarification_fields ?? [];
  const updateRuntime = (patch: Partial<WorkflowRuntimePolicy>) => {
    update({ runtime: { ...(model.runtime ?? {}), ...patch } });
  };
  const updateClarificationField = (
    index: number,
    patch: Partial<NonNullable<WorkflowRuntimePolicy['clarification_fields']>[number]>,
  ) => {
    const nextFields = clarificationFields.map((field, fieldIndex) => (
      fieldIndex === index ? { ...field, ...patch } : field
    ));
    updateRuntime({ clarification_fields: nextFields });
  };

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
      {clarificationFields.length > 0 && (
        <section className="pce-section pce-runtime-section">
          <p className="pce-section-title">{t('selfEvolutionRun.workflowConfigEditorRuntimeConfig')}</p>
          <p className="pce-section-desc">{t('selfEvolutionRun.workflowConfigEditorRuntimeConfigDesc')}</p>
          <Form layout="vertical" size="small">
            {clarificationFields.map((field, index) => (
              <div className="pce-runtime-field" key={`${field.id || index}-${index}`}>
                <Form.Item label={t('selfEvolutionRun.workflowConfigEditorConfigId')}>
                  <Input value={field.id} disabled />
                </Form.Item>
                <Form.Item label={t('selfEvolutionRun.workflowConfigEditorConfigLabel')}>
                  <Input
                    value={field.label ?? ''}
                    onChange={(e) => updateClarificationField(index, { label: e.target.value })}
                    placeholder={field.id}
                  />
                </Form.Item>
                <Form.Item label={t('selfEvolutionRun.workflowConfigEditorConfigQuestion')}>
                  <Input.TextArea
                    value={field.question}
                    onChange={(e) => updateClarificationField(index, { question: e.target.value })}
                    autoSize={{ minRows: 2, maxRows: 4 }}
                  />
                </Form.Item>
                <Form.Item label={t('selfEvolutionRun.workflowConfigEditorConfigType')}>
                  <Select
                    value={field.type ?? 'text'}
                    onChange={(value) => updateClarificationField(index, { type: value })}
                    options={[
                      { value: 'text', label: t('selfEvolutionRun.workflowConfigEditorConfigTypeText') },
                      { value: 'boolean', label: t('selfEvolutionRun.workflowConfigEditorConfigTypeBoolean') },
                      { value: 'single', label: t('selfEvolutionRun.workflowConfigEditorConfigTypeSingle') },
                      { value: 'multiple', label: t('selfEvolutionRun.workflowConfigEditorConfigTypeMultiple') },
                    ]}
                  />
                </Form.Item>
              </div>
            ))}
          </Form>
        </section>
      )}
    </div>
  );
}
