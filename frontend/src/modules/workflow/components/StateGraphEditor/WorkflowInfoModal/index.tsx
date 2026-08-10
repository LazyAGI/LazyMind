import { useEffect, useState } from 'react';
import { Modal, Input, Button, Tooltip, Spin } from 'antd';
import { QuestionCircleOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { WorkflowModel } from '../core/workflowModel';
import type { ScenarioData } from '../ScenarioEditor';
import { polishWorkflowInfo, type PolishableField } from '../../../workflowDraftApi';
import './index.scss';

const WORKFLOW_ID_REGEX = /^[a-zA-Z][a-zA-Z0-9-_]*$/;

const POLISHABLE_FIELDS: PolishableField[] = ['description', 'when_to_use', 'overview', 'notes'];

const SparkleIcon = () => (
  <svg className="pim-sparkle-icon" viewBox="0 0 16 16" fill="currentColor" xmlns="http://www.w3.org/2000/svg">
    <path d="M8 1l1.2 3.8L13 6l-3.8 1.2L8 11l-1.2-3.8L3 6l3.8-1.2L8 1z" />
    <path d="M13 9l.6 1.9L15.5 12l-1.9.6L13 15l-.6-1.9L10.5 12l1.9-.6L13 9z" opacity="0.6" />
  </svg>
);

export interface WorkflowInfoModalProps {
  open: boolean;
  onCancel: () => void;
  workflowModel: WorkflowModel;
  scenarioData: ScenarioData;
  onSave?: (pm: WorkflowModel, sd: ScenarioData) => Promise<void>;
  readonly?: boolean;
}

export default function WorkflowInfoModal({ open, onCancel, workflowModel, scenarioData, onSave, readonly = false }: WorkflowInfoModalProps) {
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);
  const [workflowId, setWorkflowId] = useState('');
  const [workflowName, setWorkflowName] = useState('');
  const [description, setDescription] = useState('');
  const [whenToUse, setWhenToUse] = useState('');
  const [overview, setOverview] = useState('');
  const [notes, setNotes] = useState('');
  const [idError, setIdError] = useState('');
  const [polishingFields, setPolishingFields] = useState<Set<PolishableField>>(new Set());
  const [polishingAll, setPolishingAll] = useState(false);

  useEffect(() => {
    if (open) {
      setWorkflowId(workflowModel.id || '');
      setWorkflowName(workflowModel.name || '');
      setDescription(workflowModel.description || '');
      setWhenToUse(workflowModel.when_to_use || '');
      setOverview(scenarioData.overview || '');
      setNotes(scenarioData.notes || '');
      setIdError('');
    }
  }, [open, workflowModel, scenarioData]);

  const validateId = (val: string) => {
    if (!val.trim()) return t('selfEvolutionRun.workflowInfoIdRequired');
    if (!WORKFLOW_ID_REGEX.test(val.trim())) return t('selfEvolutionRun.workflowInfoIdInvalid');
    return '';
  };

  const getFieldValue = (field: PolishableField): string => {
    switch (field) {
      case 'description': return description;
      case 'when_to_use': return whenToUse;
      case 'overview': return overview;
      case 'notes': return notes;
    }
  };

  const setFieldValue = (field: PolishableField, value: string) => {
    switch (field) {
      case 'description': setDescription(value); break;
      case 'when_to_use': setWhenToUse(value); break;
      case 'overview': setOverview(value); break;
      case 'notes': setNotes(value); break;
    }
  };

  const handlePolishField = async (field: PolishableField) => {
    const value = getFieldValue(field);
    if (!value.trim()) return;

    setPolishingFields(prev => new Set(prev).add(field));
    try {
      const currentFields: Partial<Record<PolishableField, string>> = {
        description, when_to_use: whenToUse, overview, notes,
      };
      const result = await polishWorkflowInfo({ fields: currentFields, target_fields: [field] });
      if (result[field]) setFieldValue(field, result[field]!);
    } catch {
    } finally {
      setPolishingFields(prev => {
        const next = new Set(prev);
        next.delete(field);
        return next;
      });
    }
  };

  const handlePolishAll = async () => {
    const currentFields: Partial<Record<PolishableField, string>> = {
      description, when_to_use: whenToUse, overview, notes,
    };
    const targetFields = POLISHABLE_FIELDS.filter(f => (currentFields[f] || '').trim() !== '');
    if (targetFields.length === 0) return;

    setPolishingAll(true);
    try {
      const result = await polishWorkflowInfo({ fields: currentFields, target_fields: targetFields });
      for (const field of targetFields) {
        if (result[field]) setFieldValue(field, result[field]!);
      }
    } catch {
    } finally {
      setPolishingAll(false);
    }
  };

  const handleSave = async () => {
    const err = validateId(workflowId);
    if (err) {
      setIdError(err);
      return;
    }
    setSaving(true);
    try {
      const newPm: WorkflowModel = {
        ...workflowModel,
        id: workflowId.trim(),
        name: workflowName.trim(),
        description: description.trim(),
        when_to_use: whenToUse.trim(),
      };
      const newSd: ScenarioData = {
        ...scenarioData,
        overview: overview.trim(),
        notes: notes.trim(),
      };
      if (onSave) await onSave(newPm, newSd);
      onCancel();
    } catch {
    } finally {
      setSaving(false);
    }
  };

  const isAnyPolishing = polishingAll || polishingFields.size > 0;

  const renderPolishIcon = (field: PolishableField, hasValue: boolean) => {
    if (readonly || !hasValue) return null;
    const isLoading = polishingFields.has(field);
    return (
      <Tooltip title={t('selfEvolutionRun.workflowInfoPolishTooltip')}>
        <button
          className={`pim-polish-btn${isLoading ? ' pim-polish-btn--loading' : ''}`}
          onClick={() => handlePolishField(field)}
          disabled={isLoading || isAnyPolishing}
          type="button"
          aria-label={t('selfEvolutionRun.workflowInfoPolishTooltip')}
        >
          {isLoading ? <Spin size="small" /> : <SparkleIcon />}
        </button>
      </Tooltip>
    );
  };

  return (
    <Modal
      title={t('selfEvolutionRun.workflowInfoModalTitle')}
      open={open}
      onCancel={onCancel}
      width={560}
      footer={
        readonly ? (
          <div className="pim-footer">
            <Button onClick={onCancel}>{t('selfEvolutionRun.workflowInfoCloseBtn')}</Button>
          </div>
        ) : (
          <div className="pim-footer">
            <Button onClick={onCancel}>{t('selfEvolutionRun.workflowInfoCancelBtn')}</Button>
            <Tooltip title={t('selfEvolutionRun.workflowInfoPolishAllTooltip')}>
              <Button
                className="pim-polish-all-btn"
                icon={<SparkleIcon />}
                loading={polishingAll}
                disabled={isAnyPolishing}
                onClick={handlePolishAll}
              >
                {t('selfEvolutionRun.workflowInfoPolishAllBtn')}
              </Button>
            </Tooltip>
            <Button type="primary" loading={saving} onClick={handleSave}>{t('selfEvolutionRun.workflowInfoSaveBtn')}</Button>
          </div>
        )
      }
      destroyOnClose
    >
      <div className="pim-body">
        {/* 插件标识 */}
        <div className="pim-row">
          <div className="pim-row-label">
            {t('selfEvolutionRun.workflowInfoFieldWorkflowId')}
            <Tooltip title={t('selfEvolutionRun.workflowInfoFieldWorkflowIdTooltip')}>
              <QuestionCircleOutlined className="pim-tip-icon" />
            </Tooltip>
          </div>
          <div className="pim-row-input">
            <Input
              value={workflowId}
              readOnly={readonly}
              onChange={(e) => {
                if (readonly) return;
                setWorkflowId(e.target.value);
                setIdError(validateId(e.target.value));
              }}
              placeholder={t('selfEvolutionRun.workflowInfoFieldWorkflowIdPlaceholder')}
              status={idError ? 'error' : undefined}
            />
            {idError && <span className="pim-field-error">{idError}</span>}
          </div>
        </div>

        {/* 显示名称 */}
        <div className="pim-row">
          <div className="pim-row-label">
            {t('selfEvolutionRun.workflowInfoFieldDisplayName')}
            <Tooltip title={t('selfEvolutionRun.workflowInfoFieldDisplayNameTooltip')}>
              <QuestionCircleOutlined className="pim-tip-icon" />
            </Tooltip>
          </div>
          <div className="pim-row-input">
            <Input
              value={workflowName}
              readOnly={readonly}
              onChange={(e) => { if (!readonly) setWorkflowName(e.target.value); }}
              placeholder={t('selfEvolutionRun.workflowInfoExamplePlaceholder')}
            />
          </div>
        </div>

        {/* 插件描述 */}
        <div className="pim-block">
          <div className="pim-block-label">
            {t('selfEvolutionRun.workflowInfoFieldDescription')}
            {renderPolishIcon('description', !!description.trim())}
          </div>
          <Input.TextArea
            value={description}
            readOnly={readonly || polishingFields.has('description') || polishingAll}
            onChange={(e) => { if (!readonly) setDescription(e.target.value); }}
            placeholder={t('selfEvolutionRun.workflowInfoFieldDescriptionPlaceholder')}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 触发条件 */}
        <div className="pim-block">
          <div className="pim-block-label">
            {t('selfEvolutionRun.workflowInfoFieldWhenToUse')}
            <Tooltip title={t('selfEvolutionRun.workflowInfoFieldWhenToUseTooltip')}>
              <QuestionCircleOutlined className="pim-tip-icon" />
            </Tooltip>
            {renderPolishIcon('when_to_use', !!whenToUse.trim())}
          </div>
          <Input.TextArea
            value={whenToUse}
            readOnly={readonly || polishingFields.has('when_to_use') || polishingAll}
            onChange={(e) => { if (!readonly) setWhenToUse(e.target.value); }}
            placeholder={t('selfEvolutionRun.workflowInfoFieldWhenToUsePlaceholder')}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 场景描述 */}
        <div className="pim-block">
          <div className="pim-block-label">
            {t('selfEvolutionRun.workflowInfoFieldOverview')}
            {renderPolishIcon('overview', !!overview.trim())}
          </div>
          <Input.TextArea
            value={overview}
            readOnly={readonly || polishingFields.has('overview') || polishingAll}
            onChange={(e) => { if (!readonly) setOverview(e.target.value); }}
            placeholder={t('selfEvolutionRun.workflowInfoFieldOverviewPlaceholder')}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 注意事项 */}
        <div className="pim-block">
          <div className="pim-block-label">
            {t('selfEvolutionRun.workflowInfoFieldNotes')}
            {renderPolishIcon('notes', !!notes.trim())}
          </div>
          <Input.TextArea
            value={notes}
            readOnly={readonly || polishingFields.has('notes') || polishingAll}
            onChange={(e) => { if (!readonly) setNotes(e.target.value); }}
            placeholder={t('selfEvolutionRun.workflowInfoFieldNotesPlaceholder')}
            autoSize={{ minRows: 2, maxRows: 4 }}
          />
        </div>
      </div>
    </Modal>
  );
}
