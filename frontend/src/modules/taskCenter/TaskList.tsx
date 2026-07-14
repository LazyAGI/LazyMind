import { useCallback, useEffect, useState } from 'react';
import { Button, Input, Progress, Segmented, Select, Table, Tooltip, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { AppstoreOutlined, CheckCircleOutlined, CloseCircleOutlined, ClockCircleOutlined, ReloadOutlined, SearchOutlined, SyncOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { listTasks } from './api';
import type { Task } from './api';
import TaskDetail, { StatusTag, formatDate } from './TaskDetail';
import { CHAT_RESUME_CONVERSATION_KEY } from '@/modules/chat/constants/chat';
import StateGraphModal from '@/components/StateGraphModal';

const PAGE_SIZE = 20;

export default function TaskList() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [statusCounts, setStatusCounts] = useState({ all: 0, waiting: 0, running: 0, succeeded: 0, failed: 0 });
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState('');
  const [type, setType] = useState('');
  const [keyword, setKeyword] = useState('');
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<Task | null>(null);
  const [graphTask, setGraphTask] = useState<Task | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await listTasks({ status: status || undefined, task_type: type || undefined, keyword: keyword || undefined, page, page_size: PAGE_SIZE });
      setTasks(response.items ?? []);
      setTotal(response.total ?? 0);
      if (response.status_counts) setStatusCounts(response.status_counts);
    } catch {
      message.error(t('taskCenter.loadError'));
    } finally {
      setLoading(false);
    }
  }, [keyword, page, status, t, type]);

  useEffect(() => { void load(); }, [load]);

  const columns: ColumnsType<Task> = [
    {
      title: t('taskCenter.tasks'),
      key: 'task',
      width: 250,
      render: (_, task) => {
        const title = task.conversation_title || task.title || t('taskCenter.noTitle');
        const description = task.title || task.schedule_name || t('taskCenter.noDescription');
        return <div className='task-name-cell'><Tooltip title={title}><strong>{truncate(title, 6)}</strong></Tooltip><Tooltip title={description}><span>{truncate(description, 15)}</span></Tooltip></div>;
      },
    },
    { title: t('taskCenter.taskType'), dataIndex: 'task_type', width: 140, render: (value) => <span className='source-tag'>{typeLabel(value, t)}</span> },
    {
      title: t('taskCenter.currentProgress'), key: 'progress', width: 190,
      render: (_, task) => {
        const done = task.steps?.filter((step) => ['completed', 'succeeded'].includes(step.status)).length ?? 0;
        const count = task.steps?.length ?? 0;
        return <div className='progress-cell'><span>{count ? `${done}/${count}` : '—'}</span><Progress percent={count ? Math.round(done / count * 100) : 0} showInfo={false} size='small' /></div>;
      },
    },
    { title: t('taskCenter.statusCol'), dataIndex: 'status', width: 130, render: (value, task) => <StatusTag status={value} onClick={task.plugin_session_id ? () => setGraphTask(task) : undefined} /> },
    { title: t('taskCenter.createdAt'), dataIndex: 'created_at', width: 190, render: formatDate },
    { title: t('taskCenter.finishedAt'), dataIndex: 'finished_at', width: 190, render: formatDate },
    { title: t('common.actions'), width: 110, render: (_, task) => <Button type='link' onClick={(event) => { event.stopPropagation(); setSelected(task); }}>{t('taskCenter.viewDetails')}</Button> },
  ];

  const statusOptions = [
    { label: <span className='status-option status-all'><AppstoreOutlined /><span>{t('taskCenter.statusAll')}</span><b>{statusCounts.all}</b></span>, value: '' },
    { label: <span className='status-option status-waiting'><ClockCircleOutlined /><span>{t('taskCenter.statusWaiting')}</span><b>{statusCounts.waiting}</b></span>, value: 'waiting' },
    { label: <span className='status-option status-running'><SyncOutlined /><span>{t('taskCenter.statusRunning')}</span><b>{statusCounts.running}</b></span>, value: 'running' },
    { label: <span className='status-option status-succeeded'><CheckCircleOutlined /><span>{t('taskCenter.statusCompleted')}</span><b>{statusCounts.succeeded}</b></span>, value: 'succeeded' },
    { label: <span className='status-option status-failed'><CloseCircleOutlined /><span>{t('taskCenter.statusFailed')}</span><b>{statusCounts.failed}</b></span>, value: 'failed' },
  ];

  const openConversation = (id: string) => {
    sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, id);
    navigate('/agent/chat/home');
  };

  return (
    <div className='all-tasks'>
      <div className='all-tasks-toolbar'>
        <Segmented className='task-status-segmented' value={status} onChange={(value) => { setStatus(String(value)); setPage(1); }} options={statusOptions} />
        <div className='all-tasks-filters'>
          <Input prefix={<SearchOutlined />} allowClear placeholder={t('taskCenter.searchPlaceholder')} value={keyword} onChange={(event) => { setKeyword(event.target.value); setPage(1); }} />
          <Select value={type} onChange={(value) => { setType(value); setPage(1); }} options={[
            { value: '', label: t('taskCenter.triggerAll') },
            { value: 'plugin_run', label: t('taskCenter.typePluginRun') },
            { value: 'background_chat', label: t('taskCenter.typeBackgroundChat') },
            { value: 'scheduled', label: t('taskCenter.typeScheduled') },
          ]} />
          <Button icon={<ReloadOutlined />} onClick={() => void load()} aria-label={t('taskCenter.refresh')} />
        </div>
      </div>
      <Table rowKey='id' className='task-table' loading={loading} columns={columns} dataSource={tasks} onRow={(task) => ({ onClick: () => setSelected(task) })} pagination={{ current: page, pageSize: PAGE_SIZE, total, onChange: setPage, showSizeChanger: false, showTotal: (value) => t('taskCenter.taskTotalItems', { total: value }) }} />
      <TaskDetail task={selected} onClose={() => setSelected(null)} onOpenConversation={openConversation} onOpenGraph={() => selected && setGraphTask(selected)} />
      {graphTask?.plugin_session_id && <StateGraphModal open onClose={() => setGraphTask(null)} sessionId={graphTask.plugin_session_id} pluginId='' liveRefresh={false} fallbackSteps={graphTask.steps} />}
    </div>
  );
}

function truncate(value: string, maxLength: number) {
  return value.length > maxLength ? `${value.slice(0, maxLength)}...` : value;
}

function typeLabel(value: string, t: (key: string) => string) {
  const labels: Record<string, string> = { plugin_run: t('taskCenter.typePluginRun'), background_chat: t('taskCenter.typeBackgroundChat'), scheduled: t('taskCenter.typeScheduled') };
  return labels[value] ?? value;
}
