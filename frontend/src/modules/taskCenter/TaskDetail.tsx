import NotificationHistory from '@/modules/notifications/NotificationHistory';
import ScheduleNotificationPanel from '@/modules/notifications/ScheduleNotificationPanel';
import '@/modules/notifications/index.scss';
import ScheduleRunHistory from './ScheduleRunHistory';
import { describeCron } from './scheduleTime';
import { useEffect, useMemo, useState } from 'react';
import { Alert, Button, Drawer, Dropdown, Empty, Modal, Tag, Tooltip } from 'antd';
import type { MenuProps } from 'antd';
import { CheckCircleFilled, CloseCircleFilled, CloseOutlined, DeleteOutlined, EllipsisOutlined, FolderOutlined, SyncOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { Task } from './api';
import { getTask } from './api';
import { CONVERSATION_TITLE_CHANGED_EVENT, type ConversationTitleChangedDetail } from '@/modules/chat/constants/chat';
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

export default function TaskDetail({ task: initialTask, onClose, onOpenConversation, onOpenGraph, onArchive, onDelete }: TaskDetailProps) {
  const { t } = useTranslation();
  const [selection, setSelection] = useState<{ sourceId: string; task: Task } | null>(null);
  const selectedTask = initialTask && selection?.sourceId === initialTask.id ? selection.task : initialTask;
  useEffect(() => { setSelection(null); }, [initialTask?.id]);
  const [detail, setDetail] = useState<Task | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [retryVersion, setRetryVersion] = useState(0);
  const task = detail?.id === selectedTask?.id ? detail : selectedTask;
  const selectedID = selectedTask?.id;
  useEffect(() => {
    const renamed = (event: Event) => {
      const { conversationId, displayName } = (event as CustomEvent<ConversationTitleChangedDetail>).detail;
      if (selectedTask?.conversation_id !== conversationId) return;
      setDetail(current => ({ ...(current?.id === selectedTask.id ? current : selectedTask), conversation_title: displayName }));
      setRetryVersion(current => current + 1);
    };
    window.addEventListener(CONVERSATION_TITLE_CHANGED_EVENT, renamed);
    return () => window.removeEventListener(CONVERSATION_TITLE_CHANGED_EVENT, renamed);
  }, [selectedTask]);
  useEffect(() => {
    setDetail((previous) => previous?.id === selectedID ? previous : null);
    setLoadFailed(false);
    if (!selectedID) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function refresh() {
      let terminal = true;
      try {
        const current = await getTask(selectedID!);
        if (!active) return;
        setDetail(current);
        setLoadFailed(false);
        terminal = ['succeeded', 'completed', 'failed', 'canceled', 'skipped'].includes(current.status);
      } catch {
        if (!active) return;
        setLoadFailed(true);
      }
      if (active && !terminal) timer = setTimeout(refresh, 5000);
    }
    void refresh();
    return () => { active = false; clearTimeout(timer); };
  }, [selectedID, retryVersion]);
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
      .catch(() => { if (active) setPlannedSteps(null); });
    return () => { active = false; };
  }, [task]);
  const steps = useMemo(() => plannedSteps?.length ? plannedSteps : task?.steps ?? [], [plannedSteps, task]);
  const taskName = task?.conversation_title || task?.title || t('taskCenter.noTitle');
  const queuedSchedule = Boolean(task?.schedule_id && task.status === 'pending');
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
      className={`task-detail-drawer${task?.schedule_id ? ' task-detail-scheduled' : ''}`}
      title={task ? <div className='task-detail-drawer-title'><strong>{taskName}</strong><span>{task.schedule_id ? t('taskCenter.scheduleRule') : <>{t('taskCenter.createdAt')} {formatDate(task.created_at)} · {taskTypeLabel(task.task_type, t)}</>}</span></div> : t('taskCenter.taskDetail')}
      width={task?.schedule_id ? 520 : 480}
      open={Boolean(task)}
      onClose={onClose}
      closable={false}
      extra={task ? <div className='task-detail-header-actions'>
        {actions.length ? <Dropdown menu={{ items: actions, onClick: handleAction }} trigger={['click']}><Button type='text' icon={<EllipsisOutlined />} aria-label={t('taskCenter.moreActions')} /></Dropdown> : null}
        <Button type='text' icon={<CloseOutlined />} aria-label={t('common.close')} onClick={onClose} />
      </div> : null}
      footer={task && queuedSchedule ? <div className='schedule-detail-actions'>
        <Button danger size='large' icon={<CloseCircleFilled />} disabled={!onDelete} onClick={() => handleAction({ key: 'trash' })}>{t('taskCenter.scheduleDelete')}</Button>
        <Button type='primary' size='large' disabled={!task.conversation_id} onClick={() => task.conversation_id && onOpenConversation(task.conversation_id)}>{t('taskCenter.scheduleEdit')}</Button>
      </div> : task ? (
        <Tooltip title={!task.conversation_id ? t('taskCenter.conversationUnavailable') : undefined}>
          <Button type='primary' block size='large' disabled={!task.conversation_id} onClick={() => task.conversation_id && onOpenConversation(task.conversation_id)}>
            {t('taskCenter.openConversation')}
          </Button>
        </Tooltip>
      ) : null}
    >
      {task ? (
        <div className='task-detail-content'>
          {loadFailed ? <Alert type='warning' showIcon message={t('taskCenter.loadError')} action={<Button size='small' onClick={() => setRetryVersion((value) => value + 1)}>{t('common.retry')}</Button>} /> : null}
          {!task.schedule_id && <div className='task-detail-status' aria-live='polite'>
            <StatusTag status={task.status} onClick={task.workflow_session_id && onOpenGraph ? () => onOpenGraph(task.workflow_session_id!) : undefined} />
          </div>}

          <section className='task-detail-section task-detail-description'>
            <h3>{t(task.schedule_id ? 'taskCenter.scheduleDescription' : 'taskCenter.taskGoal')}</h3>
            <p>{task.title || task.conversation_title || t('taskCenter.noDescription')}</p>
          </section>

          {task.schedule_id && <>
            <section className='task-detail-section task-detail-timing' aria-label={t('taskCenter.schedulePlan')}>
              {task.schedule ? <dl className='task-schedule-summary'>
                <div><dt>{t('taskCenter.scheduleTriggerPeriod')}</dt><dd><span>{describeCron(task.schedule.cron_expr, t)}</span> · <span>{task.schedule.timezone}</span></dd></div>
                <div><dt>{t('taskCenter.nextRunAt')}</dt><dd>{task.schedule.enabled ? formatScheduleDate(task.schedule.next_run_at, task.schedule.timezone) : t('taskCenter.scheduleDisabled')}</dd></div>
                <div><dt>{t('taskCenter.lastRun')}</dt><dd>{task.schedule.last_run_at ? formatScheduleDate(task.schedule.last_run_at, task.schedule.timezone) : t('taskCenter.neverExecuted')}</dd></div>
                <div><dt><Tooltip title={t('taskCenter.runCountHint')}><span tabIndex={0}>{t('taskCenter.totalExecutions')}</span></Tooltip></dt><dd>{t('taskCenter.executionCount', { count: task.schedule.run_count })}</dd></div>
              </dl> : <p>{t(detail?.id === task.id ? 'taskCenter.scheduleUnavailable' : 'taskCenter.loadingSchedule')}</p>}
            </section>
            {task.schedule && <>
              <section className='task-detail-section task-detail-notifications'>
                <ScheduleNotificationPanel key={task.schedule_id} scheduleId={task.schedule_id} taskId={task.id} title={task.schedule.name} showHistory={false} summaryCard />
              </section>
              <ScheduleRunHistory key={task.schedule_id} scheduleId={task.schedule_id} currentId={task.id} onSelect={run => {
                if (initialTask && run.id !== task.id) setSelection({ sourceId: initialTask.id, task: { ...run, schedule: task.schedule } });
              }} />
            </>}
            {!task.schedule && <section className='task-detail-section'><h3>{t('taskCenter.currentExecutionNotifications')}</h3><NotificationHistory key={task.id} taskId={task.id} /></section>}
          </>}
          {(!task.schedule_id || steps.length > 0) && <section className='task-detail-section'>
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
          </section>}
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

function formatScheduleDate(value: string, timeZone: string) {
  return new Date(value).toLocaleString(undefined, { timeZone, month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false });
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
