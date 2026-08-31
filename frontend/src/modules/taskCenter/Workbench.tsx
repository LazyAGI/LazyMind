import { useCallback, useEffect, useState } from 'react';
import { Button, Input, Progress, Select, Spin, Tooltip } from 'antd';
import { CheckCircleFilled, ClockCircleOutlined, CloseCircleOutlined, ReloadOutlined, RightOutlined, SearchOutlined, StopOutlined, SyncOutlined, UserOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { listTasks, removeTask } from './api';
import type { Task } from './api';
import TaskDetail, { StatusTag, formatDate } from './TaskDetail';
import { isTaskFinishedWithinDays } from './recentResults';
import { getChatConversationPath, selectChatConversationFilter } from '@/modules/chat/constants/chat';
import StateGraphModal from '@/components/StateGraphModal';

const SECTION_LIMIT = 5;
const ATTENTION_LIMIT = 3;

interface WorkbenchProps {
  active: boolean;
  onViewAllStatus: (status: 'failed' | 'canceled') => void;
}

export default function Workbench({ active, onViewAllStatus }: WorkbenchProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [loading, setLoading] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [type, setType] = useState('');
  const [statusCounts, setStatusCounts] = useState({ all: 0, pending: 0, waiting: 0, running: 0, succeeded: 0, failed: 0, canceled: 0 });
  const [selected, setSelected] = useState<Task | null>(null);
  const [graphTask, setGraphTask] = useState<Task | null>(null);
  const [attentionExpanded, setAttentionExpanded] = useState(false);
  const [runningExpanded, setRunningExpanded] = useState(false);
  const [recentExpanded, setRecentExpanded] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await listTasks({ keyword: keyword || undefined, task_type: type || undefined, page: 1, page_size: 60 });
      setTasks(response.items ?? []);
      if (response.status_counts) setStatusCounts(response.status_counts);
    } catch {
      // API errors are reported by the shared request interceptor.
    } finally {
      setLoading(false);
    }
  }, [keyword, type, t]);

  useEffect(() => {
    if (active) void load();
  }, [active, load]);

  const waiting = tasks.filter((task) => ['waiting', 'interrupted', 'pending'].includes(task.status));
  const running = tasks.filter((task) => task.status === 'running');
  const failed = tasks.filter((task) => task.status === 'failed');
  const canceled = tasks.filter((task) => task.status === 'canceled');
  const completed = tasks.filter((task) => ['completed', 'succeeded'].includes(task.status));
  const recentReferenceTime = Date.now();
  const recent = completed.filter((task) => isTaskFinishedWithinDays(task, 7, recentReferenceTime));
  const openConversation = (id: string) => {
    selectChatConversationFilter('task');
    navigate(getChatConversationPath(id));
  };

  return (
    <div className='workbench'>
      <div className='task-metrics'>
        <Metric icon={<UserOutlined />} tone='orange' label={t('taskCenter.needsAttention')} value={waiting.length} />
        <Metric icon={<ClockCircleOutlined />} tone='blue' label={t('taskCenter.helpingYou')} value={running.length} />
        <Metric icon={<CheckCircleFilled />} tone='green' label={t('taskCenter.completedLastSevenDays')} value={recent.length} />
        <Metric icon={<CloseCircleOutlined />} tone='red' label={t('taskCenter.statusFailed')} value={statusCounts.failed} />
        <Metric icon={<StopOutlined />} tone='gray' label={t('taskCenter.statusCanceled')} value={statusCounts.canceled} />
        <span className='task-metrics-note'>{t('taskCenter.summaryHint')}</span>
      </div>
      <div className='task-toolbar'>
        <Input prefix={<SearchOutlined />} allowClear placeholder={t('taskCenter.searchPlaceholder')} value={keyword} onChange={(event: React.ChangeEvent<HTMLInputElement>) => setKeyword(event.target.value)} />
        <Select value={type} onChange={setType} options={[
          { value: '', label: t('taskCenter.triggerAll') },
          { value: 'workflow_run', label: t('taskCenter.typeWorkflowRun') },
          { value: 'background_chat', label: t('taskCenter.typeBackgroundChat') },
          { value: 'scheduled', label: t('taskCenter.typeScheduled') },
        ]} />
        <Button icon={<ReloadOutlined />} onClick={() => void load()}>{t('taskCenter.refresh')}</Button>
      </div>

      <Spin spinning={loading}>
        <AttentionSection tasks={waiting} expanded={attentionExpanded} onToggle={() => setAttentionExpanded((value) => !value)} onSelect={setSelected} onOpenGraph={setGraphTask} />
        <RunningSection tasks={running} expanded={runningExpanded} onToggle={() => setRunningExpanded((value) => !value)} onSelect={setSelected} onOpenGraph={setGraphTask} />
        <StatusCardSection status='failed' tasks={failed} totalCount={statusCounts.failed} onViewAll={() => onViewAllStatus('failed')} onSelect={setSelected} onOpenGraph={setGraphTask} />
        <StatusCardSection status='canceled' tasks={canceled} totalCount={statusCounts.canceled} onViewAll={() => onViewAllStatus('canceled')} onSelect={setSelected} onOpenGraph={setGraphTask} />
        <RecentSection tasks={recent} expanded={recentExpanded} onToggle={() => setRecentExpanded((value) => !value)} onSelect={setSelected} />
      </Spin>
      <TaskDetail task={selected} onClose={() => setSelected(null)} onOpenConversation={openConversation} onOpenGraph={() => selected && setGraphTask(selected)} onDelete={async (task) => { await removeTask(task.id); setSelected(null); await load(); }} />
      {graphTask?.workflow_session_id && <StateGraphModal open onClose={() => setGraphTask(null)} sessionId={graphTask.workflow_session_id} workflowId='' liveRefresh={false} fallbackSteps={graphTask.steps} />}
    </div>
  );
}

function Metric({ icon, tone, label, value }: { icon: React.ReactNode; tone: string; label: string; value: number }) {
  return <div className='task-metric'><span className={`metric-icon ${tone}`}>{icon}</span><div><span>{label}</span><strong>{value}</strong></div></div>;
}

function SectionHeading({ icon, tone, title, description, count, expanded, canExpand, onToggle }: { icon: React.ReactNode; tone: string; title: string; description: string; count: number; expanded: boolean; canExpand: boolean; onToggle: () => void }) {
  const { t } = useTranslation();
  return (
    <header className='workbench-section-heading'>
      <div className='workbench-section-title'>
        <span className={`workbench-section-icon ${tone}`}>{icon}</span>
        <div><h2>{title}<span>{count}</span></h2><p>{description}</p></div>
      </div>
      {canExpand ? <Button type='link' size='small' onClick={onToggle}>{t(expanded ? 'taskCenter.collapse' : 'taskCenter.viewAll')} <RightOutlined /></Button> : null}
    </header>
  );
}

function AttentionSection({ tasks, expanded, onToggle, onSelect, onOpenGraph }: { tasks: Task[]; expanded: boolean; onToggle: () => void; onSelect: (task: Task) => void; onOpenGraph: (task: Task) => void }) {
  const { t } = useTranslation();
  return <section className='workbench-section attention'>
    <SectionHeading icon={<UserOutlined />} tone='attention' title={t('taskCenter.needsAttention')} description={t('taskCenter.needsAttentionDescription')} count={tasks.length} expanded={expanded} canExpand={tasks.length > ATTENTION_LIMIT} onToggle={onToggle} />
    {tasks.length ? <div className='attention-task-grid'>{tasks.slice(0, expanded ? undefined : ATTENTION_LIMIT).map((task) => (
      <article className='attention-task-card' key={task.id}>
        <div className='attention-task-card-top'>
          <span className={`task-type-icon task-type-${task.task_type}`}><ClockCircleOutlined /></span>
          <StatusTag status={task.status} onClick={task.workflow_session_id ? () => onOpenGraph(task) : undefined} />
        </div>
        <div className='attention-task-card-title'>
          <Tooltip title={taskTitle(task, t)}><strong>{taskTitle(task, t)}</strong></Tooltip>
          <small>{taskMeta(task, t)}</small>
        </div>
        <p>{taskDescription(task, t)}</p>
        <footer><time>{formatDate(task.updated_at)}</time><Button type='link' size='small' onClick={() => onSelect(task)}>{t('taskCenter.confirmAction')} <RightOutlined /></Button></footer>
      </article>
    ))}</div> : <WorkbenchEmpty />}
  </section>;
}

function RunningSection({ tasks, expanded, onToggle, onSelect, onOpenGraph }: { tasks: Task[]; expanded: boolean; onToggle: () => void; onSelect: (task: Task) => void; onOpenGraph: (task: Task) => void }) {
  const { t } = useTranslation();
  return <section className='workbench-section running'>
    <SectionHeading icon={<SyncOutlined spin />} tone='running' title={t('taskCenter.helpingYou')} description={t('taskCenter.helpingYouDescription')} count={tasks.length} expanded={expanded} canExpand={tasks.length > SECTION_LIMIT} onToggle={onToggle} />
    {tasks.length ? <div className='running-task-list'>
      <div className='running-task-head'><span>{t('taskCenter.tasks')}</span><span>{t('taskCenter.statusAndNext')}</span><span>{t('taskCenter.time')}</span><span /></div>
      {tasks.slice(0, expanded ? undefined : SECTION_LIMIT).map((task) => {
        const progress = taskProgress(task);
        return <button type='button' className='running-task-row' key={task.id} onClick={() => onSelect(task)}>
          <span className='task-leading-icon running'><SyncOutlined spin /></span>
          <span className='workbench-task-main'><Tooltip title={taskTitle(task, t)}><strong>{taskTitle(task, t)}</strong></Tooltip><small>{taskMeta(task, t)}</small></span>
          <span className='running-task-state'><span><StatusTag status={task.status} onClick={task.workflow_session_id ? () => onOpenGraph(task) : undefined} /><small>{taskDescription(task, t)}</small></span>{progress !== null ? <Progress percent={progress} size='small' /> : null}</span>
          <time>{formatDate(task.updated_at)}</time>
          <span className='workbench-row-action'>{t('taskCenter.viewAction')} <RightOutlined /></span>
        </button>;
      })}
    </div> : <WorkbenchEmpty />}
  </section>;
}

function StatusCardSection({ status, tasks, totalCount, onViewAll, onSelect, onOpenGraph }: { status: 'failed' | 'canceled'; tasks: Task[]; totalCount: number; onViewAll: () => void; onSelect: (task: Task) => void; onOpenGraph: (task: Task) => void }) {
  const { t } = useTranslation();
  const failed = status === 'failed';
  return <section className={`workbench-section ${status}`}>
    <SectionHeading
      icon={failed ? <CloseCircleOutlined /> : <StopOutlined />}
      tone={status}
      title={t(failed ? 'taskCenter.statusFailed' : 'taskCenter.statusCanceled')}
      description={t(failed ? 'taskCenter.failedDescription' : 'taskCenter.canceledDescription')}
      count={totalCount}
      expanded={false}
      canExpand={totalCount > Math.min(tasks.length, ATTENTION_LIMIT)}
      onToggle={onViewAll}
    />
    {tasks.length ? <div className='attention-task-grid'>{tasks.slice(0, ATTENTION_LIMIT).map((task) => (
      <article className='attention-task-card' key={task.id}>
        <div className='attention-task-card-top'>
          <span className={`task-type-icon task-type-${task.task_type}`}>{failed ? <CloseCircleOutlined /> : <StopOutlined />}</span>
          <StatusTag status={task.status} onClick={task.workflow_session_id ? () => onOpenGraph(task) : undefined} />
        </div>
        <div className='attention-task-card-title'>
          <Tooltip title={taskTitle(task, t)}><strong>{taskTitle(task, t)}</strong></Tooltip>
          <small>{taskMeta(task, t)}</small>
        </div>
        <p>{taskDescription(task, t)}</p>
        <footer><time>{formatDate(task.finished_at || task.updated_at)}</time><Button type='link' size='small' onClick={() => onSelect(task)}>{t('taskCenter.viewAction')} <RightOutlined /></Button></footer>
      </article>
    ))}</div> : <WorkbenchEmpty />}
  </section>;
}

function RecentSection({ tasks, expanded, onToggle, onSelect }: { tasks: Task[]; expanded: boolean; onToggle: () => void; onSelect: (task: Task) => void }) {
  const { t } = useTranslation();
  return <section className='workbench-section recent'>
    <SectionHeading icon={<CheckCircleFilled />} tone='recent' title={t('taskCenter.recentResults')} description={t('taskCenter.recentResultsDescription')} count={tasks.length} expanded={expanded} canExpand={tasks.length > SECTION_LIMIT} onToggle={onToggle} />
    {tasks.length ? <div className='recent-task-list'>{tasks.slice(0, expanded ? undefined : SECTION_LIMIT).map((task) => (
      <button type='button' className='recent-task-row' key={task.id} onClick={() => onSelect(task)}>
        <CheckCircleFilled className='recent-task-check' />
        <span className='workbench-task-main'><Tooltip title={taskTitle(task, t)}><strong>{taskTitle(task, t)}</strong></Tooltip><small>{taskMeta(task, t)}</small></span>
        <span className='recent-task-summary'>{taskDescription(task, t)}</span>
        <time>{formatDate(task.finished_at || task.updated_at)}</time>
        <RightOutlined className='recent-task-arrow' />
      </button>
    ))}</div> : <WorkbenchEmpty />}
  </section>;
}

function WorkbenchEmpty() {
  const { t } = useTranslation();
  return <p className='workbench-empty'>{t('taskCenter.empty')}</p>;
}

function taskTitle(task: Task, t: (key: string) => string) {
  return task.conversation_title || task.title || t('taskCenter.noTitle');
}

function taskDescription(task: Task, t: (key: string) => string) {
  return task.title || task.schedule_name || t('taskCenter.noDescription');
}

function taskMeta(task: Task, t: (key: string) => string) {
  const labels: Record<string, string> = { workflow_run: t('taskCenter.typeWorkflowRun'), background_chat: t('taskCenter.typeBackgroundChat'), scheduled: t('taskCenter.typeScheduled') };
  return labels[task.task_type] ?? task.task_type;
}

function taskProgress(task: Task) {
  if (!task.steps?.length) return null;
  const done = task.steps.filter((step) => ['completed', 'succeeded'].includes(step.status)).length;
  return Math.round((done / task.steps.length) * 100);
}
