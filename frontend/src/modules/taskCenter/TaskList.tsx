import { getLocalizedErrorMessage } from "@/components/request";
import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Input, message, Progress, Segmented, Select, Space, Table, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { AppstoreOutlined, CheckCircleOutlined, CloseCircleOutlined, DeleteOutlined, EllipsisOutlined, HourglassOutlined, PauseCircleOutlined, ReloadOutlined, SearchOutlined, StopOutlined, SyncOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import TaskRemovalDialog from './TaskRemovalDialog';
import { TASKS_REMOVED_EVENT, type TasksRemovedDetail } from './events';
import { WORKFLOW_TASKS_CHANGED } from './events';
import { listTasks } from './api';
import type { Task } from './api';
import TaskDetail, { formatDate } from './TaskDetail';
import { StatusTag } from './StatusTag';
import { CHAT_CONVERSATION_LIST_REFRESH_EVENT, CONVERSATION_TITLE_CHANGED_EVENT, getChatConversationPath, selectChatConversationFilter } from '@/modules/chat/constants/chat';
import StateGraphModal from '@/components/StateGraphModal';
import ArchiveConversationModal from '@/modules/chat/components/ArchiveConversationModal';
import { unarchiveConversation } from '@/modules/settings/recoveryApi';
import { RECOVERY_ARCHIVE_PATH } from '@/modules/settings/recoveryRoute';
import { emitConversationListRefresh } from '@/modules/chat/utils/conversationActivity';
import { taskStepProgress, useTaskSteps } from './useTaskSteps';

const PAGE_SIZE = 20;
const POLL_INTERVAL_MS = 5_000;
const TERMINAL_TASK_STATUSES = new Set(['succeeded', 'failed', 'interrupted', 'skipped', 'canceled']);

interface TaskListProps {
  active: boolean;
  status: string;
  onStatusChange: (status: string) => void;
  page: number;
  onPageChange: (page: number) => void;
}

export default function TaskList({ active, status, onStatusChange, page, onPageChange }: TaskListProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const request = useRef(0);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [statusCounts, setStatusCounts] = useState({ all: 0, pending: 0, waiting: 0, waiting_inputs: 0, running: 0, paused: 0, succeeded: 0, completed: 0, failed: 0, interrupted: 0, canceled: 0 });
  const [type, setType] = useState('');
  const [keyword, setKeyword] = useState('');
  const [loading, setLoading] = useState(false);
  const [stepsRefreshVersion, setStepsRefreshVersion] = useState(0);
  const [removingTask, setRemovingTask] = useState<Task | null>(null);
  const [selected, setSelected] = useState<Task | null>(null);
  const [archiveTask, setArchiveTask] = useState<Task | null>(null);
  const [graphTask, setGraphTask] = useState<Task | null>(null);
  const hasRunningTask = statusCounts.running > 0 || statusCounts.waiting_inputs > 0 || tasks.some((task) => task.status === 'running' || task.status === 'waiting_inputs');

  const load = useCallback(async (silent = false, refreshSteps = false) => {
    const current = ++request.current;
    if (!silent) setLoading(true);
    try {
      const response = await listTasks({ status: status || undefined, task_type: type || undefined, keyword: keyword || undefined, page, page_size: PAGE_SIZE });
      if (current !== request.current) return;
      const lastPage = Math.max(1, Math.ceil((response.total ?? 0) / PAGE_SIZE));
      if (page > lastPage) { onPageChange(lastPage); return; }
      setTasks(response.items ?? []);
      if (refreshSteps) setStepsRefreshVersion(version => version + 1);
      setTotal(response.total ?? 0);
      if (response.status_counts) setStatusCounts(current => ({ ...current, ...response.status_counts }));
    } catch {
      if (current !== request.current) return;
      // API errors are reported by the shared request interceptor.
    } finally {
      if (current === request.current) setLoading(false);
    }
  }, [keyword, page, status, type, onPageChange]);

  useEffect(() => {
    if (!active) return;
    void load();
    const renamed = () => void load();
    const removed = (event: Event) => {
      const detail = (event as CustomEvent<TasksRemovedDetail>).detail;
      ++request.current;
      setSelected(current => current && detail.removed_task_ids.includes(current.id) ? null : current);
      setGraphTask(current => current && detail.removed_task_ids.includes(current.id) ? null : current);
      void load();
    };
    window.addEventListener(TASKS_REMOVED_EVENT, removed);
    window.addEventListener(WORKFLOW_TASKS_CHANGED, renamed);
    window.addEventListener(CONVERSATION_TITLE_CHANGED_EVENT, renamed);
    window.addEventListener(CHAT_CONVERSATION_LIST_REFRESH_EVENT, renamed);
    return () => {
      ++request.current;
      window.removeEventListener(WORKFLOW_TASKS_CHANGED, renamed);
      window.removeEventListener(CONVERSATION_TITLE_CHANGED_EVENT, renamed);
      window.removeEventListener(CHAT_CONVERSATION_LIST_REFRESH_EVENT, renamed);
      window.removeEventListener(TASKS_REMOVED_EVENT, removed);
    };
  }, [active, load]);

  useEffect(() => {
    if (!active || !hasRunningTask) return;

    let cancelled = false;
    let timerId = window.setTimeout(poll, POLL_INTERVAL_MS);

    async function poll() {
      await load(true);
      if (!cancelled) timerId = window.setTimeout(poll, POLL_INTERVAL_MS);
    }

    return () => {
      cancelled = true;
      window.clearTimeout(timerId);
    };
  }, [active, hasRunningTask, load]);

  const columns: ColumnsType<Task> = [
    {
      title: t('taskCenter.tasks'),
      key: 'task',
      width: '56%',
      render: (_, task) => {
        const title = task.conversation_title || task.title || t('taskCenter.noTitle');
        const description = task.conversation_title || task.title || task.schedule_name || t('taskCenter.noDescription');
        const source = `${typeLabel(task.task_type, t)} / ${description}`;
        return <div className='task-identity-cell'><span className={`task-status-dot status-${task.status}`} /><div><Tooltip title={title}><strong>{title}</strong></Tooltip><Tooltip title={source}><span>{source}</span></Tooltip></div></div>;
      },
    },
    {
      title: t('taskCenter.statusAndNext'), key: 'state', width: '24%',
      render: (_, task) => <TaskListState task={task} refreshVersion={stepsRefreshVersion} onOpenGraph={() => setGraphTask(task)} />,
    },
    {
      title: t('taskCenter.time'),
      key: 'time',
      width: '14%',
      render: (_, task) => {
        const updatedAt = formatDate(task.finished_at || task.updated_at);
        const createdAt = `${t('taskCenter.createdAt')} ${formatDate(task.created_at)}`;
        return <div className='task-time-cell'><Tooltip title={updatedAt}><span>{updatedAt}</span></Tooltip><Tooltip title={createdAt}><small>{createdAt}</small></Tooltip></div>;
      },
    },
    { title: '', width: 104, align: 'center', render: (_, task) => <Space size={0}><Tooltip title={t('taskCenter.trashTask')}><Button danger type='text' icon={<DeleteOutlined />} aria-label={t('taskCenter.removalRowAria', { name: task.conversation_title || task.title || t('taskCenter.noTitle') })} onClick={(event: React.MouseEvent<HTMLElement>) => { event.stopPropagation(); setRemovingTask(task); }} /></Tooltip><Button type='text' icon={<EllipsisOutlined />} aria-label={t('taskCenter.viewDetails')} onClick={(event: React.MouseEvent<HTMLElement>) => { event.stopPropagation(); setSelected(task); }} /></Space> },
  ];

  const statusOptions = [
    { className: 'segment-all', label: <span className='status-option status-all'><AppstoreOutlined /><span>{t('taskCenter.statusAll')}</span><b>{statusCounts.all}</b></span>, value: '' },
    { className: 'segment-pending', label: <span className='status-option status-pending'><HourglassOutlined /><span>{t('taskCenter.statusAttention')}</span><b>{statusCounts.pending + statusCounts.waiting + statusCounts.waiting_inputs}</b></span>, value: 'attention' },
    { className: 'segment-running', label: <span className='status-option status-running'><SyncOutlined /><span>{t('taskCenter.statusRunning')}</span><b>{statusCounts.running}</b></span>, value: 'running' },
    { className: 'segment-paused', label: <span className='status-option status-paused'><PauseCircleOutlined /><span>{t('taskCenter.statusPaused')}</span><b>{statusCounts.paused}</b></span>, value: 'paused' },
    { className: 'segment-failed', label: <span className='status-option status-failed'><CloseCircleOutlined /><span>{t('taskCenter.statusFailed')}</span><b>{statusCounts.failed + statusCounts.interrupted}</b></span>, value: 'failed' },
    { className: 'segment-succeeded', label: <span className='status-option status-succeeded'><CheckCircleOutlined /><span>{t('taskCenter.statusCompleted')}</span><b>{statusCounts.succeeded + statusCounts.completed}</b></span>, value: 'succeeded' },
    { className: 'segment-canceled', label: <span className='status-option status-canceled'><StopOutlined /><span>{t('taskCenter.statusCanceled')}</span><b>{statusCounts.canceled}</b></span>, value: 'canceled' },
  ];

  const openConversation = (id: string) => {
    selectChatConversationFilter('task');
    navigate(getChatConversationPath(id));
  };

  const showArchivedFeedback = (task: Task) => {
    const messageKey = `task-archived:${task.conversation_id}`;
    message.open({
      key: messageKey,
      type: 'success',
      duration: 8,
      content: <span className='archive-feedback'>
        {t('settingsPage.recovery.archivedSuccess')}
        <Button type='link' size='small' onClick={() => {
          void unarchiveConversation(task.conversation_id)
            .then(() => {
              message.destroy(messageKey);
              message.success(t('settingsPage.recovery.unarchived'));
              emitConversationListRefresh();
            })
            .catch((error) => message.error(getLocalizedErrorMessage(error)));
        }}>{t('settingsPage.recovery.undo')}</Button>
        <Button type='link' size='small' onClick={() => navigate(RECOVERY_ARCHIVE_PATH)}>{t('settingsPage.recovery.viewArchived')}</Button>
      </span>,
    });
  };

  return (
    <div className='all-tasks'>
      <div className='all-tasks-toolbar'>
        <Segmented className='task-status-segmented' value={status} onChange={(value: string | number) => { onStatusChange(String(value)); onPageChange(1); }} options={statusOptions} />
        <div className='all-tasks-filters'>
          <Input prefix={<SearchOutlined />} allowClear placeholder={t('taskCenter.searchPlaceholder')} value={keyword} onChange={(event: React.ChangeEvent<HTMLInputElement>) => { setKeyword(event.target.value); onPageChange(1); }} />
          <Select value={type} onChange={(value: string) => { setType(value); onPageChange(1); }} options={[
            { value: '', label: t('taskCenter.triggerAll') },
            { value: 'workflow_run', label: t('taskCenter.typeWorkflowRun') },
            { value: 'background_chat', label: t('taskCenter.typeBackgroundChat') },
            { value: 'scheduled', label: t('taskCenter.typeScheduled') },
          ]} />
          <Button icon={<ReloadOutlined />} onClick={() => void load(false, true)} aria-label={t('taskCenter.refresh')} />
        </div>
      </div>
      <Table rowKey='id' className='task-table' tableLayout='fixed' loading={loading} columns={columns} dataSource={tasks} onRow={(task: Task) => ({ onClick: () => setSelected(task) })} rowClassName={(task: Task) => `task-table-row status-${task.status}`} pagination={{ current: page, pageSize: PAGE_SIZE, total, onChange: onPageChange, showSizeChanger: false, showTotal: (value: number) => t('taskCenter.taskTotalItems', { total: value }) }} />
      <TaskRemovalDialog task={removingTask} onClose={() => setRemovingTask(null)} />
      <TaskDetail task={selected} onClose={() => setSelected(null)} onOpenConversation={openConversation} onOpenGraph={(sessionId) => selected && setGraphTask({ ...selected, workflow_session_id: sessionId })} onArchive={setArchiveTask} onDelete={setRemovingTask} />
      <ArchiveConversationModal
        open={Boolean(archiveTask)}
        conversationId={archiveTask?.conversation_id}
        title={archiveTask?.conversation_title || archiveTask?.title}
        itemKind="task"
        onCancel={() => setArchiveTask(null)}
        onArchived={() => {
          const archived = archiveTask;
          setArchiveTask(null);
          setSelected(null);
          if (!archived) return;
          showArchivedFeedback(archived);
          void load();
        }}
      />
      {graphTask?.workflow_session_id && <StateGraphModal open onClose={() => setGraphTask(null)} sessionId={graphTask.workflow_session_id} workflowId='' liveRefresh={false} fallbackSteps={graphTask.steps} />}
    </div>
  );
}

function TaskListState({ task, refreshVersion, onOpenGraph }: { task: Task; refreshVersion: number; onOpenGraph: () => void }) {
  const { t } = useTranslation();
  const { steps, loading, failed } = useTaskSteps(task, refreshVersion);
  const { done, total, percent } = taskStepProgress(steps);
  const stateCopy = task.waiting_reason || (failed ? t('taskCenter.loadError') : loading ? t('common.loading')
    : total ? t('taskCenter.stepsCompleted', { done, total }) : task.conversation_title || task.title || t('taskCenter.noDescription'));
  return <div className='task-list-state'><div><StatusTag status={task.status} onClick={task.workflow_session_id ? onOpenGraph : undefined} /><Tooltip title={stateCopy}><span className='task-list-state-copy'>{stateCopy}</span></Tooltip></div>{total > 0 && !loading && !failed && !TERMINAL_TASK_STATUSES.has(task.status) ? <Progress percent={percent} showInfo={false} size='small' /> : null}</div>;
}

function typeLabel(value: string, t: (key: string) => string) {
  const labels: Record<string, string> = { workflow_run: t('taskCenter.typeWorkflowRun'), background_chat: t('taskCenter.typeBackgroundChat'), scheduled: t('taskCenter.typeScheduled') };
  return labels[value] ?? value;
}
