import { useEffect, useMemo, useState } from 'react';
import { Button, Drawer, Dropdown, Empty, Modal, Tag, Tooltip } from 'antd';
import type { MenuProps } from 'antd';
import { CheckCircleFilled, CloseOutlined, DeleteOutlined, EllipsisOutlined, FolderOutlined, SyncOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { Task } from './api';
import { axiosInstance, BASE_URL } from '@/components/request';

interface TaskDetailProps {
  task: Task | null;
  onClose: () => void;
  onOpenConversation: (conversationId: string) => void;
  onOpenGraph?: (sessionId: string) => void;
  onArchive?: (task: Task) => void;
  onDelete?: (task: Task) => Promise<void> | void;
}

const isDone = (status: string) => ['completed', 'succeeded'].includes(status);
const containsChinese = (value: string) => /[\u3400-\u4dbf\u4e00-\u9fff]/.test(value);

type PlannedStep = { step_id: string; title?: string; status: string };

export default function TaskDetail({ task, onClose, onOpenConversation, onOpenGraph, onArchive, onDelete }: TaskDetailProps) {
  const { t } = useTranslation();
  const [plannedSteps, setPlannedSteps] = useState<PlannedStep[] | null>(null);
  useEffect(() => {
    setPlannedSteps(null);
    if (!task?.workflow_session_id) return;
    let active = true;
    axiosInstance.get(`${BASE_URL}/api/core/workflow-sessions/${encodeURIComponent(task.workflow_session_id)}/projection`, { silentError: true } as never)
      .then((response) => {
        if (!active) return;
        const payload = response.data?.data ?? response.data;
        const order: string[] = payload?.graph?.static_order ?? Object.keys(payload?.graph?.nodes ?? {});
        const nodes = payload?.projection?.nodes ?? {};
        const current: string[] = payload?.projection?.current ?? [];
        setPlannedSteps(order.filter((id) => id !== '__start__' && id !== '__end__').map((id) => ({
          step_id: payload?.graph?.nodes?.[id]?.label || id,
          status: current.includes(id) ? 'running' : nodes[id]?.execution || 'pending',
        })));
      })
      .catch(() => setPlannedSteps(null));
    return () => { active = false; };
  }, [task]);
  const steps = useMemo(() => plannedSteps ?? task?.steps ?? [], [plannedSteps, task]);
  const taskName = task?.conversation_title || task?.title || t('taskCenter.noTitle');
  const actions: NonNullable<MenuProps['items']> = task ? [
    ...(onArchive ? [{ key: 'archive', icon: <FolderOutlined />, label: t('settingsPage.recovery.archiveAction'), disabled: !task.conversation_id }] : []),
    ...(onDelete ? [{ key: 'trash', icon: <DeleteOutlined />, label: t('taskCenter.trashTask'), danger: true }] : []),
  ] : [];

  const handleAction = ({ key }: { key: string }) => {
    if (!task) return;
    if (key === 'archive') {
      onArchive?.(task);
      return;
    }
    if (key === 'trash' && onDelete) {
      Modal.confirm({
        title: t('taskCenter.trashTaskTitle', { name: taskName }),
        content: t('taskCenter.trashTaskDescription'),
        okText: t('settingsPage.recovery.moveToTrash'),
        cancelText: t('common.cancel'),
        okButtonProps: { danger: true },
        onOk: () => onDelete(task),
      });
    }
  };

  return (
    <Drawer
      className='task-detail-drawer'
      title={task ? <div className='task-detail-drawer-title'><strong>{taskName}</strong><span>{t('taskCenter.createdAt')} {formatDate(task.created_at)} · {taskTypeLabel(task.task_type, t)}</span></div> : t('taskCenter.taskDetail')}
      width={480}
      open={Boolean(task)}
      onClose={onClose}
      closable={false}
      extra={task ? <div className='task-detail-header-actions'>
        {actions.length ? <Dropdown menu={{ items: actions, onClick: handleAction }} trigger={['click']}><Button type='text' icon={<EllipsisOutlined />} aria-label={t('taskCenter.moreActions')} /></Dropdown> : null}
        <Button type='text' icon={<CloseOutlined />} aria-label={t('common.close')} onClick={onClose} />
      </div> : null}
      footer={task ? (
        <Tooltip title={!task.conversation_id ? t('taskCenter.conversationUnavailable') : undefined}>
          <Button type='primary' block size='large' disabled={!task.conversation_id} onClick={() => task.conversation_id && onOpenConversation(task.conversation_id)}>
            {t('taskCenter.openConversation')}
          </Button>
        </Tooltip>
      ) : null}
    >
      {task ? (
        <div className='task-detail-content'>
          <div className='task-detail-status'>
            <StatusTag status={task.status} onClick={task.workflow_session_id && onOpenGraph ? () => onOpenGraph(task.workflow_session_id!) : undefined} />
          </div>

          <section className='task-detail-section task-detail-description'>
            <h3>{t('taskCenter.taskGoal')}</h3>
            <p>{task.title || task.conversation_title || t('taskCenter.noDescription')}</p>
          </section>

          <section className='task-detail-section'>
            <h3>{t('taskCenter.executionSteps')}</h3>
            {steps.length ? (
              <div className='task-step-list'>
                {steps.map((step, index) => (
                  <div className={`task-step ${isDone(step.status) ? 'is-done' : step.status === 'running' ? 'is-running' : step.status === 'failed' ? 'is-failed' : ''}`} key={`${step.step_id}-${index}`}>
                    <span className='task-step-dot'>{isDone(step.status) ? <CheckCircleFilled /> : step.status === 'running' ? <SyncOutlined spin /> : index + 1}</span>
                    <div><strong>{taskStepLabel(step.title || step.step_id, index, t)}</strong><small>{taskStatusLabel(step.status, t)}</small></div>
                  </div>
                ))}
              </div>
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={task.waiting_reason || t('taskCenter.noSteps')} />}
          </section>
        </div>
      ) : null}
    </Drawer>
  );
}

export function StatusTag({ status, onClick }: { status: string; onClick?: () => void }) {
  const { t } = useTranslation();
  const color = isDone(status) ? 'success' : status === 'failed' ? 'error' : status === 'running' ? 'processing' : 'warning';
  const key = status === 'succeeded' ? 'Completed' : status === 'waiting_inputs' ? 'WaitingInputs' : `${status.charAt(0).toUpperCase()}${status.slice(1)}`;
  return <Tag className={onClick ? 'clickable-status' : undefined} color={color} onClick={(event: React.MouseEvent<HTMLElement>) => { event.stopPropagation(); onClick?.(); }}>{t(`taskCenter.status${key}`, { defaultValue: status })}</Tag>;
}

export function formatDate(value?: string) {
  return value ? new Date(value).toLocaleString() : '—';
}

function taskTypeLabel(taskType: string, t: (key: string, options?: Record<string, unknown>) => string) {
  const labels: Record<string, string> = {
    workflow_run: t('taskCenter.typeWorkflowRun'),
    background_chat: t('taskCenter.typeBackgroundChat'),
    scheduled: t('taskCenter.typeScheduled'),
  };
  return labels[taskType] ?? t('taskCenter.typeOther');
}

function taskStepLabel(value: string, index: number, t: (key: string, options?: Record<string, unknown>) => string) {
  const normalized = value.trim().toLowerCase().replace(/[\s-]+/g, '_');
  const labels: Record<string, string> = {
    prepare: t('taskCenter.stepPrepare'),
    preparation: t('taskCenter.stepPrepare'),
    outline: t('taskCenter.stepOutline'),
    write: t('taskCenter.stepWriteDocument'),
    writing: t('taskCenter.stepWriteDocument'),
    write_document: t('taskCenter.stepWriteDocument'),
    plan: t('taskCenter.stepPlan'),
    planning: t('taskCenter.stepPlan'),
    research: t('taskCenter.stepResearch'),
    search: t('taskCenter.stepSearch'),
    retrieve: t('taskCenter.stepSearch'),
    draft: t('taskCenter.stepDraft'),
    review: t('taskCenter.stepReview'),
    finalize: t('taskCenter.stepFinalize'),
    finish: t('taskCenter.stepFinalize'),
  };
  if (labels[normalized]) return labels[normalized];
  if (containsChinese(value)) return value;
  return t('taskCenter.stepFallback', { index: index + 1 });
}

function taskStatusLabel(status: string, t: (key: string, options?: Record<string, unknown>) => string) {
  const labels: Record<string, string> = {
    completed: t('taskCenter.statusCompleted'),
    succeeded: t('taskCenter.statusSucceeded'),
    failed: t('taskCenter.statusFailed'),
    running: t('taskCenter.statusRunning'),
    pending: t('taskCenter.statusPending'),
    interrupted: t('taskCenter.statusInterrupted'),
    waiting: t('taskCenter.statusWaiting'),
    waiting_inputs: t('taskCenter.statusWaitingInputs'),
    canceled: t('taskCenter.statusCanceled'),
    skipped: t('taskCenter.statusSkipped'),
    ready: t('taskCenter.statusReady'),
    blocked: t('taskCenter.statusBlocked'),
    stale: t('taskCenter.statusStale'),
    pruned: t('taskCenter.statusSkipped'),
    bypassed: t('taskCenter.statusSkipped'),
    none: t('taskCenter.statusPending'),
  };
  return labels[status] ?? t('taskCenter.statusUnknown');
}
