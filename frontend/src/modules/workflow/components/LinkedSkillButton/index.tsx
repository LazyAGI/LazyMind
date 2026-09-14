import { useEffect, useState } from 'react';
import { Alert, Button, Descriptions, Modal, Space, Spin, Tag } from 'antd';
import { ThunderboltOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { getSkillAssetDetail, listBuiltinSkills } from '@/modules/memory/skillApi';
import type { SkillAssetRecord } from '@/modules/memory/skillApi';
import { listSkillLinkedWorkflows } from '../../workflowDraftApi';
import type { SkillLinkedWorkflow, WorkflowDraftRecord } from '../../workflowDraftApi';
import NewWorkflowModal from '../NewWorkflowModal';
import './index.scss';

interface Props {
  draft: Pick<WorkflowDraftRecord, 'id' | 'name' | 'source_skill_id' | 'source_skill_name' | 'published' | 'published_workflow_ref'>;
  conversionDisabled?: boolean;
  onCreated: (draftId: string) => void;
}

type DetailState =
  | { status: 'loading' }
  | { status: 'error' }
  | { status: 'ready'; skill: SkillAssetRecord; workflows: SkillLinkedWorkflow[] };

export default function LinkedSkillButton({ draft, conversionDisabled = false, onCreated }: Props) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [retry, setRetry] = useState(0);
  const [detail, setDetail] = useState<DetailState>({ status: 'loading' });
  const [conversionSkill, setConversionSkill] = useState<{ id: string; name: string } | null>(null);
  const skillId = draft.source_skill_id;
  const isBuiltin = skillId.startsWith('builtin:');

  useEffect(() => {
    if (!open || !skillId) return;
    let cancelled = false;
    setDetail({ status: 'loading' });
    const skillRequest = isBuiltin
      ? listBuiltinSkills().then((skills) => skills.find((skill) => skill.id === skillId.slice('builtin:'.length)) ?? null)
      : getSkillAssetDetail(skillId, { loadContent: false });
    void Promise.all([skillRequest, listSkillLinkedWorkflows(skillId)])
      .then(([skill, linked]) => {
        if (cancelled) return;
        setDetail(skill ? { status: 'ready', skill, workflows: linked.workflows } : { status: 'error' });
      })
      .catch(() => { if (!cancelled) setDetail({ status: 'error' }); });
    return () => { cancelled = true; };
  }, [open, skillId, isBuiltin, retry]);

  if (!skillId) return null;

  const skill = detail.status === 'ready' ? detail.skill : null;
  const workflows = detail.status === 'ready' ? detail.workflows : [];
  const hasCurrentWorkflow = workflows.some((workflow) => workflow.workflow_ref === draft.published_workflow_ref);
  const workflowStatus = (workflow: SkillLinkedWorkflow) => !workflow.enabled
    ? t('selfEvolutionRun.linkedSkillDisabled')
    : workflow.available
      ? t('selfEvolutionRun.linkedSkillEnabled')
      : t('selfEvolutionRun.linkedSkillUnavailable');

  return (
    <>
      <Button size="small" onClick={() => setOpen(true)}>{t('selfEvolutionRun.linkedSkillButton')}</Button>
      <Modal
        title={t('admin.memorySkillDetailTitle')}
        open={open}
        onCancel={() => setOpen(false)}
        footer={<Button onClick={() => setOpen(false)}>{t('common.close')}</Button>}
        width={540}
        centered
        className="workflow-linked-skill-modal"
      >
        {detail.status === 'loading' ? (
          <div className="workflow-linked-skill-loading" role="status"><Spin /><span>{t('common.loading')}</span></div>
        ) : detail.status === 'error' ? (
          <Alert
            type="warning"
            showIcon
            message={t('selfEvolutionRun.linkedSkillLoadFailed')}
            description={draft.source_skill_name || undefined}
            action={<Button size="small" onClick={() => setRetry((value) => value + 1)}>{t('common.retry')}</Button>}
          />
        ) : (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <Descriptions column={1} size="small" items={[
              { key: 'name', label: t('admin.memoryName'), children: detail.skill.name },
              { key: 'description', label: t('admin.memoryDescription'), children: detail.skill.description || '—' },
              { key: 'category', label: t('admin.memoryCategory'), children: detail.skill.category || '—' },
              { key: 'status', label: t('selfEvolutionRun.linkedSkillStatus'), children: (
                <Tag color={detail.skill.deletedAt ? 'error' : detail.skill.isEnabled ? 'success' : 'default'}>
                  {t(detail.skill.deletedAt ? 'selfEvolutionRun.linkedSkillDeleted' : isBuiltin ? 'selfEvolutionRun.linkedSkillBuiltin' : detail.skill.isEnabled ? 'selfEvolutionRun.linkedSkillEnabled' : 'selfEvolutionRun.linkedSkillDisabled')}
                </Tag>
              ) },
              { key: 'workflows', label: t('selfEvolutionRun.linkedSkillWorkflows'), children: (
                <div className="workflow-linked-skill-workflows">
                  {!hasCurrentWorkflow && (
                    <div>
                      <Button type="link" size="small" onClick={() => setOpen(false)}>{draft.name}</Button>
                      <Tag>{t(draft.published ? 'selfEvolutionRun.linkedSkillPublished' : 'selfEvolutionRun.linkedSkillDraft')}</Tag>
                    </div>
                  )}
                  {workflows.map((workflow) => (
                    <div key={workflow.workflow_ref}>
                      {workflow.workflow_ref === draft.published_workflow_ref
                        ? <Button type="link" size="small" onClick={() => setOpen(false)}>{workflow.name}</Button>
                        : <span>{workflow.name}</span>}
                      <Tag color={workflow.available ? 'success' : 'default'}>{workflowStatus(workflow)}</Tag>
                    </div>
                  ))}
                </div>
              ) },
            ]} />
            <Alert type="info" showIcon message={t('selfEvolutionRun.linkedSkillIndependentHint')} />
            <Button
              type="primary"
              block
              icon={<ThunderboltOutlined />}
              disabled={conversionDisabled || !!detail.skill.deletedAt}
              onClick={() => {
                if (conversionDisabled || !skill || skill.deletedAt) return;
                setOpen(false);
                setConversionSkill({ id: skillId, name: skill.name });
              }}
            >{t('selfEvolutionRun.linkedSkillReconvert')}</Button>
            <div className="workflow-linked-skill-hint">{t('selfEvolutionRun.linkedSkillReconvertHint')}</div>
          </Space>
        )}
      </Modal>
      {conversionSkill && (
        <NewWorkflowModal
          open
          initialSkill={conversionSkill}
          onCancel={() => setConversionSkill(null)}
          onCreated={(draftId) => { setConversionSkill(null); onCreated(draftId); }}
        />
      )}
    </>
  );
}
