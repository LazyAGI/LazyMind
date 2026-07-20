import { useCallback, useEffect, useState } from 'react';
import { Button, Input, Progress, Segmented, Select, Table, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { AppstoreOutlined, CheckCircleOutlined, CloseCircleOutlined, ClockCircleOutlined, EllipsisOutlined, HourglassOutlined, ReloadOutlined, SearchOutlined, StopOutlined, SyncOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { listTasks } from './api';
import type { Task } from './api';
import TaskDetail, { StatusTag, formatDate } from './TaskDetail';
import { CHAT_RESUME_CONVERSATION_KEY } from '@/modules/chat/constants/chat';
import StateGraphModal from '@/components/StateGraphModal';

const PAGE_SIZE = 20;
const POLL_INTERVAL_MS = 5_000;

interface TaskListProps {
  active: boolean;
}

export default function TaskList({ active }: TaskListProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [statusCounts, setStatusCounts] = useState({ all: 0, pending: 0, waiting: 0, running: 0, succeeded: 0, failed: 0, canceled: 0 });
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState('');
  const [type, setType] = useState('');
  const [keyword, setKeyword] = useState('');
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<Task | null>(null);
  const [graphTask, setGraphTask] = useState<Task | null>(null);
  const hasRunningTask = statusCounts.running > 0 || tasks.some((task) => task.status === 'running');

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
      width: '40%',
      render: (_, task) => {
        const title = task.conversation_title || task.title || t('taskCenter.noTitle');
        const description = task.title || task.schedule_name || t('taskCenter.noDescription');
        return <div className='task-identity-cell'><span className={`task-status-dot status-${task.status}`} /><div><Tooltip title={title}><strong>{title}</strong></Tooltip><span>{typeLabel(task.task_type, t)} / {description}</span></div></div>;
      },
    },
    {
      title: t('taskCenter.statusAndNext'), key: 'state', width: '34%',
      render: (_, task) => {
        const done = task.steps?.filter((step) => ['completed', 'succeeded'].includes(step.status)).length ?? 0;
        const count = task.steps?.length ?? 0;
        return <div className='task-list-state'><div><StatusTag status={task.status} onClick={task.plugin_session_id ? () => setGraphTask(task) : undefined} /><span>{count ? t('taskCenter.stepsCompleted', { done, total: count }) : task.title || t('taskCenter.noDescription')}</span></div>{count ? <Progress percent={Math.round(done / count * 100)} showInfo={false} size='small' /> : null}</div>;
      },
    },
    { title: t('taskCenter.time'), key: 'time', width: '20%', render: (_, task) => <div className='task-time-cell'><span>{formatDate(task.finished_at || task.updated_at)}</span><small>{t('taskCenter.createdAt')} {formatDate(task.created_at)}</small></div> },
    { title: '', width: 56, align: 'center', render: (_, task) => <Button type='text' icon={<EllipsisOutlined />} aria-label={t('taskCenter.viewDetails')} onClick={(event: React.MouseEvent<HTMLElement>) => { event.stopPropagation(); setSelected(task); }} /> },
  ];

  const statusOptions = [
    { label: <span className='status-option status-all'><AppstoreOutlined /><span>{t('taskCenter.statusAll')}</span><b>{statusCounts.all}</b></span>, value: '' },
    { label: <span className='status-option status-pending'><HourglassOutlined /><span>{t('taskCenter.statusPending')}</span><b>{statusCounts.pending}</b></span>, value: 'pending' },
    { label: <span className='status-option status-waiting'><ClockCircleOutlined /><span>{t('taskCenter.statusWaiting')}</span><b>{statusCounts.waiting}</b></span>, value: 'waiting' },
    { label: <span className='status-option status-running'><SyncOutlined /><span>{t('taskCenter.statusRunning')}</span><b>{statusCounts.running}</b></span>, value: 'running' },
    { label: <span className='status-option status-succeeded'><CheckCircleOutlined /><span>{t('taskCenter.statusCompleted')}</span><b>{statusCounts.succeeded}</b></span>, value: 'succeeded' },
    { label: <span className='status-option status-failed'><CloseCircleOutlined /><span>{t('taskCenter.statusFailed')}</span><b>{statusCounts.failed}</b></span>, value: 'failed' },
    { label: <span className='status-option status-canceled'><StopOutlined /><span>{t('taskCenter.statusCanceled')}</span><b>{statusCounts.canceled}</b></span>, value: 'canceled' },
  ];

  const openConversation = (id: string) => {
    sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, id);
    navigate('/agent/chat/home');
  };

  return (
    <div className='all-tasks'>
      <div className='all-tasks-toolbar'>
        <Segmented className='task-status-segmented' value={status} onChange={(value: string | number) => { setStatus(String(value)); setPage(1); }} options={statusOptions} />
        <div className='all-tasks-filters'>
          <Input prefix={<SearchOutlined />} allowClear placeholder={t('taskCenter.searchPlaceholder')} value={keyword} onChange={(event: React.ChangeEvent<HTMLInputElement>) => { setKeyword(event.target.value); setPage(1); }} />
          <Select value={type} onChange={(value: string) => { setType(value); setPage(1); }} options={[
            { value: '', label: t('taskCenter.triggerAll') },
            { value: 'plugin_run', label: t('taskCenter.typePluginRun') },
            { value: 'background_chat', label: t('taskCenter.typeBackgroundChat') },
            { value: 'scheduled', label: t('taskCenter.typeScheduled') },
          ]} />
          <Button icon={<ReloadOutlined />} onClick={() => void load()} aria-label={t('taskCenter.refresh')} />
        </div>
      </div>
      <Table rowKey='id' className='task-table' loading={loading} columns={columns} dataSource={tasks} onRow={(task: Task) => ({ onClick: () => setSelected(task) })} rowClassName={(task: Task) => `task-table-row status-${task.status}`} pagination={{ current: page, pageSize: PAGE_SIZE, total, onChange: setPage, showSizeChanger: false, showTotal: (value: number) => t('taskCenter.taskTotalItems', { total: value }) }} />
      <TaskDetail task={selected} onClose={() => setSelected(null)} onOpenConversation={openConversation} onOpenGraph={() => selected && setGraphTask(selected)} />
      {graphTask?.plugin_session_id && <StateGraphModal open onClose={() => setGraphTask(null)} sessionId={graphTask.plugin_session_id} pluginId='' liveRefresh={false} fallbackSteps={graphTask.steps} />}
    </div>
  );
}

function typeLabel(value: string, t: (key: string) => string) {
  const labels: Record<string, string> = { plugin_run: t('taskCenter.typePluginRun'), background_chat: t('taskCenter.typeBackgroundChat'), scheduled: t('taskCenter.typeScheduled') };
  return labels[value] ?? value;
}
