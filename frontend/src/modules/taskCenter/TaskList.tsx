import { useCallback, useEffect, useState } from 'react';
import { Button, Input, Progress, Segmented, Select, Table, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { AppstoreOutlined, CheckCircleOutlined, CloseCircleOutlined, ClockCircleOutlined, ReloadOutlined, SearchOutlined, SyncOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { listTasks } from './api';
import type { Task } from './api';
import TaskDetail, { StatusTag, formatDate } from './TaskDetail';
import { CHAT_RESUME_CONVERSATION_KEY } from '@/modules/chat/constants/chat';

const PAGE_SIZE = 20;

export default function TaskList() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState('');
  const [type, setType] = useState('');
  const [keyword, setKeyword] = useState('');
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<Task | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await listTasks({ status: status || undefined, task_type: type || undefined, keyword: keyword || undefined, page, page_size: PAGE_SIZE });
      setTasks(response.items ?? []);
      setTotal(response.total ?? 0);
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
      render: (_, task) => <div className='task-name-cell'><strong>{task.conversation_title || task.title || t('taskCenter.noTitle')}</strong><span>{task.title || task.schedule_name || t('taskCenter.noDescription')}</span></div>,
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
    { title: t('taskCenter.statusCol'), dataIndex: 'status', width: 130, render: (value) => <StatusTag status={value} /> },
    { title: t('taskCenter.createdAt'), dataIndex: 'created_at', width: 190, render: formatDate },
    { title: t('taskCenter.finishedAt'), dataIndex: 'finished_at', width: 190, render: formatDate },
    { title: t('common.actions'), width: 110, render: (_, task) => <Button type='link' onClick={(event) => { event.stopPropagation(); setSelected(task); }}>{t('taskCenter.viewDetails')}</Button> },
  ];

  const statusOptions = [
    { label: <><AppstoreOutlined /> {t('taskCenter.statusAll')}</>, value: '' },
    { label: <><ClockCircleOutlined /> {t('taskCenter.statusWaiting')}</>, value: 'waiting' },
    { label: <><SyncOutlined /> {t('taskCenter.statusRunning')}</>, value: 'running' },
    { label: <><CheckCircleOutlined /> {t('taskCenter.statusCompleted')}</>, value: 'succeeded' },
    { label: <><CloseCircleOutlined /> {t('taskCenter.statusFailed')}</>, value: 'failed' },
  ];

  const openConversation = (id: string) => {
    sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, id);
    navigate('/agent/chat/home');
  };

  return (
    <div className='all-tasks'>
      <Segmented className='task-status-segmented' value={status} onChange={(value) => { setStatus(String(value)); setPage(1); }} options={statusOptions} />
      <div className='task-toolbar'>
        <Input prefix={<SearchOutlined />} allowClear placeholder={t('taskCenter.searchPlaceholder')} value={keyword} onChange={(event) => { setKeyword(event.target.value); setPage(1); }} />
        <Select value={type} onChange={(value) => { setType(value); setPage(1); }} options={[
          { value: '', label: t('taskCenter.triggerAll') },
          { value: 'plugin_run', label: t('taskCenter.typePluginRun') },
          { value: 'background_chat', label: t('taskCenter.typeBackgroundChat') },
          { value: 'scheduled', label: t('taskCenter.typeScheduled') },
        ]} />
        <Button icon={<ReloadOutlined />} onClick={() => void load()}>{t('taskCenter.refresh')}</Button>
      </div>
      <Table rowKey='id' className='task-table' loading={loading} columns={columns} dataSource={tasks} onRow={(task) => ({ onClick: () => setSelected(task) })} pagination={{ current: page, pageSize: PAGE_SIZE, total, onChange: setPage, showSizeChanger: false, showTotal: (value) => t('taskCenter.taskTotalItems', { total: value }) }} />
      <TaskDetail task={selected} onClose={() => setSelected(null)} onOpenConversation={openConversation} />
    </div>
  );
}

function typeLabel(value: string, t: (key: string) => string) {
  const labels: Record<string, string> = { plugin_run: t('taskCenter.typePluginRun'), background_chat: t('taskCenter.typeBackgroundChat'), scheduled: t('taskCenter.typeScheduled') };
  return labels[value] ?? value;
}
