import { useCallback, useEffect, useState } from 'react';
import { Button, Empty, Input, Progress, Select, Spin, Tooltip } from 'antd';
import { CheckCircleFilled, ClockCircleOutlined, DownOutlined, ReloadOutlined, SearchOutlined, UpOutlined, UserOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { listTasks } from './api';
import type { Task } from './api';
import TaskDetail, { StatusTag, formatDate } from './TaskDetail';
import { CHAT_RESUME_CONVERSATION_KEY } from '@/modules/chat/constants/chat';
import StateGraphModal from '@/components/StateGraphModal';

const SECTION_LIMIT = 5;

interface WorkbenchProps {
  active: boolean;
}

export default function Workbench({ active }: WorkbenchProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [loading, setLoading] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [type, setType] = useState('');
  const [selected, setSelected] = useState<Task | null>(null);
  const [graphTask, setGraphTask] = useState<Task | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await listTasks({ keyword: keyword || undefined, task_type: type || undefined, page: 1, page_size: 60 });
      setTasks(response.items ?? []);
    } catch {
      // API errors are reported by the shared request interceptor.
    } finally {
      setLoading(false);
    }
  }, [keyword, type, t]);

  useEffect(() => {
    if (active) void load();
  }, [active, load]);

  const waiting = tasks.filter((task) => ['waiting', 'interrupted', 'pending', 'failed'].includes(task.status));
  const running = tasks.filter((task) => task.status === 'running');
  const recent = tasks.filter((task) => ['completed', 'succeeded'].includes(task.status));
  const openConversation = (id: string) => {
    sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, id);
    navigate('/agent/chat/home');
  };

  return (
    <div className='workbench'>
      <div className='task-metrics'>
        <Metric icon={<UserOutlined />} tone='orange' label={t('taskCenter.needsAttention')} value={waiting.length} />
        <Metric icon={<ClockCircleOutlined />} tone='blue' label={t('taskCenter.helpingYou')} value={running.length} />
        <Metric icon={<CheckCircleFilled />} tone='green' label={t('taskCenter.completedToday')} value={recent.length} />
      </div>
      <div className='task-toolbar'>
        <Input prefix={<SearchOutlined />} allowClear placeholder={t('taskCenter.searchPlaceholder')} value={keyword} onChange={(event) => setKeyword(event.target.value)} />
        <Select value={type} onChange={setType} options={[
          { value: '', label: t('taskCenter.triggerAll') },
          { value: 'plugin_run', label: t('taskCenter.typePluginRun') },
          { value: 'background_chat', label: t('taskCenter.typeBackgroundChat') },
          { value: 'scheduled', label: t('taskCenter.typeScheduled') },
        ]} />
        <Button icon={<ReloadOutlined />} onClick={() => void load()}>{t('taskCenter.refresh')}</Button>
      </div>

      <Spin spinning={loading}>
        <WorkbenchSection title={t('taskCenter.needsAttention')} count={waiting.length} tasks={waiting} limit={SECTION_LIMIT} onSelect={setSelected} onOpenGraph={setGraphTask} variant='attention' />
        <WorkbenchSection title={t('taskCenter.helpingYou')} count={running.length} tasks={running} limit={SECTION_LIMIT} onSelect={setSelected} onOpenGraph={setGraphTask} variant='running' />
        <WorkbenchSection title={t('taskCenter.recentResults')} count={recent.length} tasks={recent} limit={SECTION_LIMIT} onSelect={setSelected} onOpenGraph={setGraphTask} variant='recent' />
      </Spin>
      <TaskDetail task={selected} onClose={() => setSelected(null)} onOpenConversation={openConversation} onOpenGraph={() => selected && setGraphTask(selected)} />
      {graphTask?.plugin_session_id && <StateGraphModal open onClose={() => setGraphTask(null)} sessionId={graphTask.plugin_session_id} pluginId='' liveRefresh={false} fallbackSteps={graphTask.steps} />}
    </div>
  );
}

function Metric({ icon, tone, label, value }: { icon: React.ReactNode; tone: string; label: string; value: number }) {
  return <div className='task-metric'><span className={`metric-icon ${tone}`}>{icon}</span><div><span>{label}</span><strong>{value}</strong></div></div>;
}

function WorkbenchSection({ title, count, tasks, limit, onSelect, onOpenGraph, variant }: { title: string; count: number; tasks: Task[]; limit: number; onSelect: (task: Task) => void; onOpenGraph: (task: Task) => void; variant: string }) {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState(false);
  return (
    <section className={`workbench-section ${variant} ${collapsed ? 'is-collapsed' : ''}`}>
      <header><h3>{title}<span>{count}</span></h3><Button type='text' size='small' icon={collapsed ? <DownOutlined /> : <UpOutlined />} onClick={() => setCollapsed((value) => !value)} aria-label={collapsed ? t('taskCenter.expand') : t('taskCenter.collapse')} /></header>
      {!collapsed && <div className='workbench-scroll'>
        {tasks.length ? tasks.slice(0, Math.max(limit, tasks.length)).map((task) => (
          <button className='workbench-task' key={task.id} onClick={() => onSelect(task)}>
            <span className={`task-type-icon task-type-${task.task_type}`}><ClockCircleOutlined /></span>
            <span className='workbench-task-main'><Tooltip title={task.conversation_title || task.title}><strong>{task.conversation_title || task.title || t('taskCenter.noTitle')}</strong></Tooltip><Tooltip title={task.title || task.schedule_name}><small>{task.title || task.schedule_name || t('taskCenter.noDescription')}</small></Tooltip></span>
            <span className='workbench-task-progress'>{task.steps?.length ? <Progress percent={Math.round((task.steps.filter((step) => ['completed', 'succeeded'].includes(step.status)).length / task.steps.length) * 100)} size='small' showInfo={false} /> : null}</span>
            <StatusTag status={task.status} onClick={task.plugin_session_id ? () => onOpenGraph(task) : undefined} />
            <time>{formatDate(task.updated_at)}</time>
          </button>
        )) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('taskCenter.empty')} />}
      </div>}
    </section>
  );
}
