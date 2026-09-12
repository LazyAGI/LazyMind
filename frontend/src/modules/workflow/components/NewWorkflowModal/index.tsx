import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { v4 as uuidv4 } from 'uuid';
import { Modal, Input, Button, Select, Tooltip, message, Alert, Spin } from 'antd';
import { FileTextOutlined, ThunderboltOutlined, BulbOutlined, QuestionCircleOutlined, CheckCircleOutlined, ExclamationCircleOutlined, DownOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { createWorkflowDraft, aiGenerateWorkflowDraft, updateWorkflowDraftContent, preflightSkillWorkflowConversion, listWorkflowDrafts } from '../../workflowDraftApi';
import type { SkillWorkflowPreflightResponse, WorkflowDraftRecord } from '../../workflowDraftApi';
import { listSkillAssetsPage } from '@/modules/memory/skillApi';
import { serializeWorkflowModel } from '../StateGraphEditor/core/workflowSerializer';
import { createEmptyWorkflowModel } from '../StateGraphEditor/core/workflowModel';
import './index.scss';

const WORKFLOW_ID_REGEX = /^[a-zA-Z][a-zA-Z0-9-_]*$/;

function skillNameSlug(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 48);
}

function newSkillWorkflowId(name: string): string {
  const slug = skillNameSlug(name) || 'workflow';
  const prefix = /^[a-z]/.test(slug) ? slug : `workflow-${slug}`;
  return `${prefix}-${uuidv4().slice(0, 8)}`;
}

type CreateMode = 'blank' | 'ai' | 'skill';

interface NewWorkflowModalProps {
  open: boolean;
  onCancel: () => void;
  onCreated: (draftId: string) => void;
  initialSkill?: { id: string; name: string };
}

export default function NewWorkflowModal({ open, onCancel, onCreated, initialSkill }: NewWorkflowModalProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const formId = useId();
  const session = useRef(0);
  const skillRequest = useRef(0);
  const nameEdited = useRef(false);

  const MODE_CARDS: { mode: CreateMode; icon: React.ReactNode; title: string; desc: string; badge?: string }[] = [
    {
      mode: 'ai',
      icon: <BulbOutlined />,
      title: t('selfEvolutionRun.newWorkflowModeAiTitle'),
      desc: t('selfEvolutionRun.newWorkflowModeAiDesc'),
    },
    {
      mode: 'skill',
      icon: <ThunderboltOutlined />,
      title: t('selfEvolutionRun.newWorkflowModeSkillTitle'),
      desc: t('selfEvolutionRun.newWorkflowModeSkillDesc'),
    },
    {
      mode: 'blank',
      icon: <FileTextOutlined />,
      title: t('selfEvolutionRun.newWorkflowModeBlankTitle'),
      desc: t('selfEvolutionRun.newWorkflowModeBlankDesc'),
      badge: t('selfEvolutionRun.newWorkflowModeBlankBadge'),
    },
  ];
  const [mode, setMode] = useState<CreateMode>('ai');

  // skill mode: selected skill
  const [skillId, setSkillId] = useState<string | undefined>(undefined);
  const [skillName, setSkillName] = useState('');
  const [skillOptions, setSkillOptions] = useState<{ label: string; value: string }[]>([]);
  const [skillLoading, setSkillLoading] = useState(false);

  // fields shown after skill is selected (or always for ai/blank)
  const [workflowId, setWorkflowId] = useState('');
  const [idError, setIdError] = useState('');
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');

  const [creating, setCreating] = useState(false);
  const [preflightLoading, setPreflightLoading] = useState(false);
  const [preflight, setPreflight] = useState<SkillWorkflowPreflightResponse | null>(null);
  const [preflightError, setPreflightError] = useState('');
  const [preflightRetry, setPreflightRetry] = useState(0);
  const [checksExpanded, setChecksExpanded] = useState(false);
  const [linkedWorkflows, setLinkedWorkflows] = useState<WorkflowDraftRecord[]>([]);
  const [linkedLoading, setLinkedLoading] = useState(false);
  const [linkedError, setLinkedError] = useState(false);
  const [linkedRetry, setLinkedRetry] = useState(0);
  const [linkedOpen, setLinkedOpen] = useState(false);

  // For skill mode: fields appear only after skill is chosen
  const skillSelected = mode === 'skill' && !!skillId;
  const showFields = mode === 'ai' || mode === 'blank' || skillSelected;

  const reset = useCallback((preset?: NewWorkflowModalProps['initialSkill'], nextMode: CreateMode = 'ai') => {
    session.current += 1;
    skillRequest.current += 1;
    setMode(preset ? 'skill' : nextMode);
    setSkillId(preset?.id);
    setSkillName(preset?.name ?? '');
    setSkillOptions(preset ? [{ label: preset.name, value: preset.id }] : []);
    setSkillLoading(false);
    setWorkflowId(preset ? newSkillWorkflowId(preset.name) : '');
    setIdError('');
    setName(preset ? t('selfEvolutionRun.newWorkflowSuggestedName', { name: preset.name.replace(/助手$/, '') }).slice(0, 60) : '');
    nameEdited.current = false;
    setDescription('');
    setCreating(false);
    setPreflight(null);
    setPreflightError('');
    setPreflightLoading(false);
    setChecksExpanded(false);
    setLinkedWorkflows([]);
    setLinkedLoading(false);
    setLinkedError(false);
    setLinkedOpen(false);
  }, [t]);

  const initialSkillId = initialSkill?.id;
  const initialSkillName = initialSkill?.name;
  useEffect(() => {
    reset(open && initialSkillId ? { id: initialSkillId, name: initialSkillName ?? '' } : undefined);
    return () => {
      session.current += 1;
      skillRequest.current += 1;
    };
  }, [open, initialSkillId, initialSkillName, reset]);

  useEffect(() => {
    if (!open || mode !== 'skill' || !skillId) {
      setPreflight(null);
      setPreflightError('');
      setPreflightLoading(false);
      return;
    }
    let cancelled = false;
    const activeSession = session.current;
    const isCurrent = () => !cancelled && session.current === activeSession;
    setChecksExpanded(false);
    setPreflightLoading(true);
    setPreflight(null);
    setPreflightError('');
    preflightSkillWorkflowConversion(skillId)
      .then((result) => {
        if (isCurrent()) setPreflight(result);
      })
      .catch(() => {
        if (isCurrent()) setPreflightError(t('selfEvolutionRun.newWorkflowPreflightFailed'));
      })
      .finally(() => {
        if (isCurrent()) setPreflightLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, mode, skillId, initialSkillId, initialSkillName, preflightRetry, t]);

  useEffect(() => {
    if (!open || mode !== 'skill' || !skillId) return;
    let cancelled = false;
    const activeSession = session.current;
    const isCurrent = () => !cancelled && session.current === activeSession;
    setLinkedLoading(true);
    setLinkedError(false);
    setLinkedWorkflows([]);
    setLinkedOpen(false);
    void (async () => {
      try {
        const drafts: WorkflowDraftRecord[] = [];
        for (let page = 1; ; page += 1) {
          const result = await listWorkflowDrafts({ page, pageSize: 100 });
          if (!isCurrent()) return;
          drafts.push(...result.records);
          if (!result.records.length || drafts.length >= result.total) break;
        }
        setLinkedWorkflows(drafts.filter((draft) => draft.source_skill_id === skillId));
        if (!nameEdited.current) {
          const base = t('selfEvolutionRun.newWorkflowSuggestedName', { name: skillName.replace(/助手$/, '') }).slice(0, 60);
          let suggested = base;
          let number = 2;
          const names = new Set(drafts.map((draft) => draft.name));
          while (names.has(suggested)) {
            const suffix = t('selfEvolutionRun.newWorkflowSuggestedNameCopy', { number: number++ });
            suggested = `${base.slice(0, Math.max(0, 60 - suffix.length))}${suffix}`;
          }
          setName(suggested);
        }
      } catch {
        if (isCurrent()) setLinkedError(true);
      } finally {
        if (isCurrent()) setLinkedLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [open, mode, skillId, skillName, initialSkillName, linkedRetry, t]);

  const handleCancel = () => {
    reset();
    onCancel();
  };

  const handleViewWorkflow = (event: React.MouseEvent<HTMLElement>, draftId: string) => {
    event.preventDefault();
    handleCancel();
    navigate(`/memory-management/workflows/${encodeURIComponent(draftId)}`);
  };

  const handleSkillSearch = async (keyword: string) => {
    const requestId = ++skillRequest.current;
    setSkillLoading(true);
    try {
      const result = await listSkillAssetsPage({ keyword, page: 1, pageSize: 20, excludeBuiltinTemplates: true });
      if (requestId === skillRequest.current) {
        setSkillOptions(result.records.map((r) => ({ label: r.name, value: r.id })));
      }
    } catch {
      // ignore
    } finally {
      if (requestId === skillRequest.current) setSkillLoading(false);
    }
  };

  const handleSkillChange = (val: string | undefined, option?: { label: string; value: string } | { label: string; value: string }[]) => {
    setSkillId(val);
    const opt = Array.isArray(option) ? option[0] : option;
    setSkillName(opt?.label ?? '');
    setWorkflowId(val ? newSkillWorkflowId(opt?.label ?? '') : '');
    setIdError('');
    setName(val ? t('selfEvolutionRun.newWorkflowSuggestedName', { name: (opt?.label ?? '').replace(/助手$/, '') }).slice(0, 60) : '');
    nameEdited.current = false;
    setChecksExpanded(false);
    setPreflight(null);
    setPreflightError('');
  };

  const handleModeChange = (newMode: CreateMode) => {
    reset(undefined, newMode);
  };

  const handleCreate = async () => {
    if (!open || creating) return;
    const trimmedId = workflowId.trim();
    if (!trimmedId) {
      setIdError(t('selfEvolutionRun.newWorkflowIdErrorEmpty'));
      return;
    }
    if (!WORKFLOW_ID_REGEX.test(trimmedId)) {
      setIdError(t('selfEvolutionRun.newWorkflowIdErrorInvalid'));
      return;
    }
    if (mode === 'ai' && !description.trim()) {
      message.warning(t('selfEvolutionRun.newWorkflowDescRequired'));
      return;
    }
    if (mode === 'skill' && !skillId) {
      message.warning(t('selfEvolutionRun.newWorkflowSkillRequired'));
      return;
    }
    if (mode === 'skill' && (linkedLoading || !name.trim() || name.trim().length > 60)) return;
    if (mode === 'skill' && preflightLoading) {
      message.warning(t('selfEvolutionRun.newWorkflowPreflightRunning'));
      return;
    }
    if (mode === 'skill' && preflight?.status === 'blocked') {
      message.warning(t('selfEvolutionRun.newWorkflowPreflightBlocked'));
      return;
    }

    // Display name falls back to workflow id if empty
    const effectiveName = name.trim() || trimmedId;

    setCreating(true);
    const activeSession = session.current;
    const isCurrent = () => session.current === activeSession;
    let draftId: string | undefined;
    try {
      const draft = await createWorkflowDraft({ name: effectiveName, source_type: mode });
      if (!isCurrent()) return;
      draftId = draft.id;
      const pm = { ...createEmptyWorkflowModel(), id: trimmedId, name: effectiveName };
      await updateWorkflowDraftContent(draft.id, {
        workflow_yaml_content: serializeWorkflowModel(pm),
        version: draft.version,
      });
      if (!isCurrent()) return;
      if (mode === 'ai') {
        await aiGenerateWorkflowDraft(draft.id, { description: description.trim() });
      } else if (mode === 'skill' && skillId) {
        await aiGenerateWorkflowDraft(draft.id, { skill_id: skillId });
      }
      if (!isCurrent()) return;
      draftId = undefined;
      reset();
      onCreated(draft.id);
    } catch {
      if (!isCurrent()) return;
      if (draftId) {
        message.warning(t('selfEvolutionRun.workflowDetailFailedBanner'));
        onCreated(draftId);
        draftId = undefined;
      }
    } finally {
      if (isCurrent()) setCreating(false);
    }
  };

  const preflightBlocked = mode === 'skill' && preflight?.status === 'blocked';
  const canCreate = showFields && workflowId.trim() !== '' && !idError && !preflightLoading && !preflightBlocked && (mode !== 'skill' || (!linkedLoading && !!name.trim() && name.trim().length <= 60));
  const preflightIssues = preflight?.checks?.filter((check) => check.severity === 'error' || check.severity === 'warning') ?? [];
  const preflightErrors = preflightIssues.filter((check) => check.severity === 'error').length;
  const preflightWarnings = preflightIssues.filter((check) => check.severity === 'warning').length;

  const idField = (
    <div className="npm-field-row">
      <label className="npm-field-label" htmlFor={`${formId}-workflow-id`}>
        {t('selfEvolutionRun.newWorkflowFieldWorkflowId')} <span className="npm-required-mark">*</span>
        <Tooltip title={t('selfEvolutionRun.newWorkflowFieldWorkflowIdTooltip')}>
          <QuestionCircleOutlined className="npm-tip-icon" />
        </Tooltip>
      </label>
      <div className="npm-field-input">
        <Input
          id={`${formId}-workflow-id`}
          value={workflowId}
          disabled={creating}
          onChange={(e) => {
            setWorkflowId(e.target.value);
            setIdError(e.target.value.trim() && !WORKFLOW_ID_REGEX.test(e.target.value.trim())
              ? t('selfEvolutionRun.newWorkflowIdErrorInvalid') : '');
          }}
          placeholder={t('selfEvolutionRun.newWorkflowFieldWorkflowIdPlaceholder')}
          status={idError ? 'error' : undefined}
          aria-invalid={!!idError}
          aria-describedby={idError ? `${formId}-id-error` : undefined}
          onPressEnter={() => void handleCreate()}
        />
        {idError && <span id={`${formId}-id-error`} role="alert" className="npm-field-error">{idError}</span>}
      </div>
    </div>
  );

  return (
    <>
    <Modal
      title={t('selfEvolutionRun.newWorkflowModalTitle')}
      open={open}
      onCancel={handleCancel}
      footer={
        <div className="npm-footer">
          {mode === 'skill' && <span className="npm-footer-hint"><SafetyCertificateOutlined /> {t('selfEvolutionRun.newWorkflowSourcePreserved')}</span>}
          <Button onClick={handleCancel}>{t('selfEvolutionRun.newWorkflowCancelBtn')}</Button>
          <Button type="primary" loading={creating} disabled={!canCreate} onClick={() => void handleCreate()}>
            {t(mode !== 'skill' ? 'selfEvolutionRun.newWorkflowCreateBtn'
              : linkedWorkflows.length ? 'selfEvolutionRun.linkedSkillReconvert' : 'selfEvolutionRun.newWorkflowStartConversion')}
          </Button>
        </div>
      }
      className="new-workflow-modal"
      width={skillSelected ? 640 : 520}
      style={{ top: 24 }}
      destroyOnClose
    >
      <div className="npm-body">
        <p className="npm-section-label">{t('selfEvolutionRun.newWorkflowSelectMode')}</p>
        <div className="npm-mode-cards">
          {MODE_CARDS.map((card) => (
            <button
              key={card.mode}
              type="button"
              className={`npm-mode-card${mode === card.mode ? ' npm-mode-card--active' : ''}`}
              aria-pressed={mode === card.mode}
              disabled={creating}
              onClick={() => handleModeChange(card.mode)}
            >
              {card.badge && <span className="npm-mode-badge">{card.badge}</span>}
              <span className="npm-mode-icon">{card.icon}</span>
              <span className="npm-mode-title">{card.title}</span>
              <span className="npm-mode-desc">{card.desc}</span>
            </button>
          ))}
        </div>

        {mode === 'skill' && <>
          <p className="npm-hint">{t('selfEvolutionRun.newWorkflowConversionHint')}</p>
          <Select
            showSearch
            allowClear
            aria-label={t('selfEvolutionRun.newWorkflowSkillSearchPlaceholder')}
            placeholder={t('selfEvolutionRun.newWorkflowSkillSearchPlaceholder')}
            value={skillId}
            onChange={handleSkillChange}
            onSearch={handleSkillSearch}
            loading={skillLoading}
            disabled={creating}
            options={skillId && !skillOptions.some((option) => option.value === skillId)
              ? [{ label: skillName || skillId, value: skillId }, ...skillOptions]
              : skillOptions}
            filterOption={false}
            style={{ width: '100%' }}
            onFocus={() => skillOptions.length === 0 && void handleSkillSearch('')}
          />
          {skillSelected && linkedLoading && <div className="npm-linked-note" role="status"><Spin size="small" /><span>{t('selfEvolutionRun.newWorkflowLinkedLoading')}</span></div>}
          {skillSelected && linkedError && <div className="npm-linked-note" role="status">
            <ExclamationCircleOutlined /><span>{t('selfEvolutionRun.newWorkflowLinkedFailed')}</span>
            <Button type="link" size="small" onClick={() => setLinkedRetry((value) => value + 1)}>{t('common.retry')}</Button>
          </div>}
          {skillSelected && linkedWorkflows.length > 0 && <div className="npm-linked-note" role="status">
            <CheckCircleOutlined className="npm-check-pass" />
            <span>{t('selfEvolutionRun.newWorkflowAlreadyLinked')}</span>
            {linkedWorkflows.length === 1
              ? <Button type="link" size="small" href={`/memory-management/workflows/${encodeURIComponent(linkedWorkflows[0].id)}`} onClick={(event) => handleViewWorkflow(event, linkedWorkflows[0].id)}>{t('common.view')}</Button>
              : <Button type="link" size="small" onClick={() => setLinkedOpen(true)}>{t('selfEvolutionRun.newWorkflowViewLinked', { count: linkedWorkflows.length })}</Button>}
            <span className="npm-hint">{t('selfEvolutionRun.newWorkflowLinkedPreserved')}</span>
          </div>}
        </>}

        {mode === 'ai' && <Input.TextArea
          aria-label={t('selfEvolutionRun.newWorkflowModeAiTitle')}
          placeholder={t('selfEvolutionRun.newWorkflowAiPlaceholder')}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          autoSize={{ minRows: 5, maxRows: 10 }}
        />}

        {showFields && <div className="npm-fields npm-expand">
          {mode !== 'skill' && idField}
          <div className="npm-field-row">
            <label className="npm-field-label" htmlFor={`${formId}-name`}>
              {t('selfEvolutionRun.newWorkflowFieldDisplayName')}
              {mode === 'skill' ? <span className="npm-required-mark">*</span> : <Tooltip title={t('selfEvolutionRun.newWorkflowFieldDisplayNameTooltip')}><QuestionCircleOutlined className="npm-tip-icon" /></Tooltip>}
            </label>
            <Input
              id={`${formId}-name`}
              aria-required={mode === 'skill'}
              value={name}
              maxLength={mode === 'skill' ? 60 : undefined}
              disabled={creating}
              onChange={(e) => { nameEdited.current = true; setName(e.target.value); }}
              placeholder={mode === 'skill' || !workflowId.trim()
                ? t('selfEvolutionRun.newWorkflowFieldDisplayNamePlaceholder')
                : t('selfEvolutionRun.newWorkflowFieldDisplayNamePlaceholderWithId', { id: workflowId.trim() })}
              onPressEnter={() => void handleCreate()}
            />
          </div>
          {mode === 'skill' && <details className="npm-advanced" open={idError ? true : undefined}>
            <summary>{t('selfEvolutionRun.newWorkflowAdvancedId')}</summary>
            {idField}
          </details>}
        </div>}

        {skillSelected && <>
          {preflightLoading && <div className="npm-preflight npm-preflight--loading" role="status">
            <Spin size="small" /><span>{t('selfEvolutionRun.newWorkflowPreflightLoading')}</span>
          </div>}
          {!preflightLoading && preflightError && <Alert
            type="warning"
            showIcon
            message={preflightError}
            description={t('selfEvolutionRun.newWorkflowPreflightFallback')}
            action={<Button size="small" onClick={() => setPreflightRetry((value) => value + 1)}>{t('common.retry')}</Button>}
          />}
          {!preflightLoading && preflight && <div className="npm-preflight-panel">
            <div className={`npm-check-summary${preflightIssues.length || preflightBlocked ? ' is-warning' : ''}`}>
              <span role="status">
                {preflightIssues.length || preflightBlocked ? <ExclamationCircleOutlined /> : <CheckCircleOutlined />}
                {t(preflightBlocked ? 'selfEvolutionRun.newWorkflowPreflightBlockedTitle'
                  : preflight.status === 'warning' ? 'selfEvolutionRun.newWorkflowPreflightWarningTitle'
                    : 'selfEvolutionRun.newWorkflowPreflightPassedTitle', { errors: preflightErrors, warnings: preflightWarnings })}
              </span>
              <Button type="link" size="small" aria-expanded={checksExpanded} aria-controls={`${formId}-checks`} onClick={() => setChecksExpanded((value) => !value)}>
                {t(checksExpanded ? 'selfEvolutionRun.newWorkflowHideChecks' : 'selfEvolutionRun.newWorkflowShowChecks')}
                <DownOutlined rotate={checksExpanded ? 180 : 0} />
              </Button>
            </div>
            {preflight.summary && <p className="npm-preflight-summary">{preflight.summary}</p>}
            {preflightIssues.length > 0 && <div className="npm-check-issues" role="alert">
              {preflightIssues.map((check, index) => <div className="npm-check-issue" key={`${check.code}:${check.path}:${index}`}>
                <ExclamationCircleOutlined />
                <div><span>{check.message}</span>{check.suggestion && <small>{check.suggestion}</small>}</div>
              </div>)}
            </div>}
            <div id={`${formId}-checks`} className="npm-check-details" role="region" aria-label={t('selfEvolutionRun.newWorkflowCheckDetails')} hidden={!checksExpanded}>
              {checksExpanded && (preflight.checks?.length ? preflight.checks.map((check, index) => <div className="npm-check-row" key={`${check.code}:${check.path}:${index}`}>
                {check.severity === 'error' || check.severity === 'warning' ? <ExclamationCircleOutlined className="npm-check-warning" /> : <CheckCircleOutlined className="npm-check-pass" />}
                <div><span>{check.path || t('selfEvolutionRun.newWorkflowCheckContent')}</span><small>{check.message}</small>{check.suggestion && <small>{check.suggestion}</small>}</div>
              </div>) : <div className="npm-check-row">
                <CheckCircleOutlined className="npm-check-pass" /><div><span>{t('selfEvolutionRun.newWorkflowCheckContent')}</span><small>{preflight.summary || t('selfEvolutionRun.newWorkflowPreflightPassedTitle')}</small></div>
              </div>)}
            </div>
          </div>}
          <Alert type={preflightBlocked ? 'warning' : 'info'} showIcon message={t(preflightBlocked ? 'selfEvolutionRun.newWorkflowResolveBeforeConversion' : 'selfEvolutionRun.newWorkflowInitiallyDisabled')} />
        </>}
      </div>
    </Modal>
    <Modal
      title={t('selfEvolutionRun.linkedSkillWorkflows')}
      open={open && linkedOpen}
      onCancel={() => setLinkedOpen(false)}
      footer={<Button onClick={() => setLinkedOpen(false)}>{t('common.close')}</Button>}
      width={540}
    >
      <div className="npm-linked-workflows">
        {linkedWorkflows.map((workflow) => <div key={workflow.id}>
          <Button type="link" href={`/memory-management/workflows/${encodeURIComponent(workflow.id)}`} onClick={(event) => handleViewWorkflow(event, workflow.id)}>{workflow.name}</Button>
          <span>{t(workflow.published ? 'selfEvolutionRun.linkedSkillPublished' : 'selfEvolutionRun.linkedSkillDraft')}</span>
        </div>)}
      </div>
    </Modal>
    </>
  );
}
