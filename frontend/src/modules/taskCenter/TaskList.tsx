import { useCallback, useEffect, useState } from 'react';
import { Button, Input, message, Progress, Segmented, Select, Table, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { AppstoreOutlined, CheckCircleOutlined, CloseCircleOutlined, ClockCircleOutlined, EllipsisOutlined, HourglassOutlined, ReloadOutlined, SearchOutlined, StopOutlined, SyncOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { listTasks, removeTask } from './api';
import type { Task } from './api';
import TaskDetail, { StatusTag, formatDate } from './TaskDetail';
import { getChatConversationPath, selectChatConversationFilter } from '@/modules/chat/constants/chat';
import StateGraphModal from '@/components/StateGraphModal';
import ArchiveConversationModal from '@/modules/chat/components/ArchiveConversationModal';
import { unarchiveConversation } from '@/modules/settings/recoveryApi';
import { RECOVERY_ARCHIVE_PATH } from '@/modules/settings/recoveryRoute';

const PAGE_SIZE = 20;
const POLL_INTERVAL_MS = 5_000;
const TERMINAL_TASK_STATUSES = new Set(['succeeded', 'failed', 'skipped', 'canceled']);

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
  const [tasks, setTasks] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [statusCounts, setStatusCounts] = useState({ all: 0, pending: 0, waiting: 0, waiting_inputs: 0, running: 0, succeeded: 0, failed: 0, canceled: 0 });
  const [type, setType] = useState('');
  const [keyword, setKeyword] = useState('');
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<Task | null>(null);
  const [archiveTask, setArchiveTask] = useState<Task | null>(null);
  const [graphTask, setGraphTask] = useState<Task | null>(null);
  const hasRunningTask = statusCounts.running > 0 || statusCounts.waiting_inputs > 0 || tasks.some((task) => task.status === 'running' || task.status === 'waiting_inputs');

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      const response = await listTasks({ status: status || undefined, task_type: type || undefined, keyword: keyword || undefined, page, page_size: PAGE_SIZE });
      setTasks(response.items ?? []);
      setTotal(response.total ?? 0);
      if (response.status_counts) setStatusCounts(response.status_counts);
    } catch {
      // API errors are reported by the shared request interceptor.
    } finally {
      if (!silent) setLoading(false);
    }
  }, [keyword, page, status, t, type]);

  useEffect(() => {
    if (active) void load();
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
      render: (_, task) => {
        const done = task.steps?.filter((step) => ['completed', 'succeeded'].includes(step.status)).length ?? 0;
        const count = task.steps?.length ?? 0;
        const showProgress = count > 0 && !TERMINAL_TASK_STATUSES.has(task.status);
        const stateCopy = task.waiting_reason || (count ? t('taskCenter.stepsCompleted', { done, total: count }) : task.conversation_title || task.title || t('taskCenter.noDescription'));
        return <div className='task-list-state'><div><StatusTag status={task.status} onClick={task.workflow_session_id ? () => setGraphTask(task) : undefined} /><Tooltip title={stateCopy}><span className='task-list-state-copy'>{stateCopy}</span></Tooltip></div>{showProgress ? <Progress percent={Math.round(done / count * 100)} showInfo={false} size='small' /> : null}</div>;
      },
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
    { title: '', width: 56, align: 'center', render: (_, task) => <Button type='text' icon={<EllipsisOutlined />} aria-label={t('taskCenter.viewDetails')} onClick={(event: React.MouseEvent<HTMLElement>) => { event.stopPropagation(); setSelected(task); }} /> },
  ];

  const statusOptions = [
    { label: <span className='status-option status-all'><AppstoreOutlined /><span>{t('taskCenter.statusAll')}</span><b>{statusCounts.all}</b></span>, value: '' },
    { label: <span className='status-option status-pending'><HourglassOutlined /><span>{t('taskCenter.statusPending')}</span><b>{statusCounts.pending}</b></span>, value: 'pending' },
    { label: <span className='status-option status-waiting'><ClockCircleOutlined /><span>{t('taskCenter.statusWaiting')}</span><b>{statusCounts.waiting}</b></span>, value: 'waiting' },
    { label: <span className='status-option status-waiting'><ClockCircleOutlined /><span>{t('taskCenter.statusWaitingInputs')}</span><b>{statusCounts.waiting_inputs}</b></span>, value: 'waiting_inputs' },
    { label: <span className='status-option status-running'><SyncOutlined /><span>{t('taskCenter.statusRunning')}</span><b>{statusCounts.running}</b></span>, value: 'running' },
    { label: <span className='status-option status-succeeded'><CheckCircleOutlined /><span>{t('taskCenter.statusCompleted')}</span><b>{statusCounts.succeeded}</b></span>, value: 'succeeded' },
    { label: <span className='status-option status-failed'><CloseCircleOutlined /><span>{t('taskCenter.statusFailed')}</span><b>{statusCounts.failed}</b></span>, value: 'failed' },
    { label: <span className='status-option status-canceled'><StopOutlined /><span>{t('taskCenter.statusCanceled')}</span><b>{statusCounts.canceled}</b></span>, value: 'canceled' },
  ];

  const openConversation = (id: string) => {
    selectChatConversationFilter('task');
    navigate(getChatConversationPath(id));
  };

  const handleDelete = async (task: Task) => {
    try {
      await removeTask(task.id);
    } catch {
      message.error(t('taskCenter.taskRemoveFailed'));
      return;
    }
    setSelected(null);
    message.success(t('taskCenter.taskRemoveSuccess'));
    if (tasks.length === 1 && page > 1) {
      onPageChange(page - 1);
      return;
    }
    await load();
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
              void load();
            })
            .catch(() => message.error(t('settingsPage.recovery.operationFailed')));
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
          <Button icon={<ReloadOutlined />} onClick={() => void load()} aria-label={t('taskCenter.refresh')} />
        </div>
      </div>
      <Table rowKey='id' className='task-table' tableLayout='fixed' loading={loading} columns={columns} dataSource={tasks} onRow={(task: Task) => ({ onClick: () => setSelected(task) })} rowClassName={(task: Task) => `task-table-row status-${task.status}`} pagination={{ current: page, pageSize: PAGE_SIZE, total, onChange: onPageChange, showSizeChanger: false, showTotal: (value: number) => t('taskCenter.taskTotalItems', { total: value }) }} />
      <TaskDetail task={selected} onClose={() => setSelected(null)} onOpenConversation={openConversation} onOpenGraph={() => selected && setGraphTask(selected)} onArchive={setArchiveTask} onDelete={handleDelete} />
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

function typeLabel(value: string, t: (key: string) => string) {
  const labels: Record<string, string> = { workflow_run: t('taskCenter.typeWorkflowRun'), background_chat: t('taskCenter.typeBackgroundChat'), scheduled: t('taskCenter.typeScheduled') };
  return labels[value] ?? value;
}
