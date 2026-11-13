import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Alert, Button, Empty, Input, Pagination, Progress, Select, Spin, Tooltip } from 'antd';
import { CheckCircleFilled, ClockCircleOutlined, CloseCircleOutlined, PauseCircleOutlined, ReloadOutlined, RightOutlined, SearchOutlined, StopOutlined, SyncOutlined, UserOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import TaskRemovalDialog from './TaskRemovalDialog';
import { TASKS_REMOVED_EVENT, type TasksRemovedDetail } from './events';
import { getWorkbench } from './api';
import type { Task, WorkbenchCategory, WorkbenchResponse } from './api';
import TaskDetail, { formatDate } from './TaskDetail';
import { StatusTag } from './StatusTag';
import { CONVERSATION_TITLE_CHANGED_EVENT, getChatConversationPath, selectChatConversationFilter } from '@/modules/chat/constants/chat';
import StateGraphModal from '@/components/StateGraphModal';
import { WORKFLOW_TASKS_CHANGED } from './events';
import { taskStepProgress, useTaskSteps } from './useTaskSteps';

const CATEGORIES: WorkbenchCategory[] = ['attention', 'running', 'paused', 'failed', 'recent', 'canceled'];

interface WorkbenchProps {
  active: boolean;
}

export default function Workbench({ active }: WorkbenchProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const request = useRef(0);
  const resultsRef = useRef<HTMLDivElement>(null);
  const [data, setData] = useState<WorkbenchResponse | null>(null);
  const [pagination, setPagination] = useState<{ category?: WorkbenchCategory; page: number; page_size: number }>({ page: 1, page_size: 4 });
  const [loading, setLoading] = useState(false);
  const [stepsRefreshVersion, setStepsRefreshVersion] = useState(0);
  const [loadFailed, setLoadFailed] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [type, setType] = useState('');
  const [removingTask, setRemovingTask] = useState<Task | null>(null);
  const [selected, setSelected] = useState<Task | null>(null);
  const [graphTask, setGraphTask] = useState<Task | null>(null);
  const [activeCategory, setActiveCategory] = useState<WorkbenchCategory>('attention');

  useLayoutEffect(() => {
    if (!active) return;
    const results = resultsRef.current!;
    const viewport = results.closest<HTMLElement>('.task-center-page');
    if (!viewport) return;
    const measure = () => {
      if (!results.clientWidth || !viewport.clientHeight) return;
      const style = getComputedStyle(results);
      const columns = Number(style.getPropertyValue('--workbench-columns'));
      const minCardHeight = parseFloat(style.getPropertyValue('--workbench-card-min-height'));
      const maxCardHeight = parseFloat(style.getPropertyValue('--workbench-card-max-height'));
      const gap = parseFloat(style.getPropertyValue('--workbench-card-gap'));
      const chrome = parseFloat(style.getPropertyValue('--workbench-page-chrome'));
      // Use the unscrolled position: scrolling must not change the page size.
      const top = results.getBoundingClientRect().top - viewport.getBoundingClientRect().top + viewport.scrollTop;
      const available = viewport.clientHeight - top - chrome;
      const rows = Math.min(Math.max(1, Math.floor((available + gap) / (minCardHeight + gap))), Math.floor(100 / columns));
      const cardHeight = Math.max(minCardHeight, Math.min(maxCardHeight, (available - (rows - 1) * gap) / rows));
      results.style.setProperty('--workbench-card-height', `${Math.floor(cardHeight)}px`);
      const pageSize = columns * rows;
      setPagination(current => current.page_size === pageSize ? current : { page: 1, page_size: pageSize });
    };
    measure();
    const observer = new ResizeObserver(measure);
    [viewport, results, results.parentElement!].forEach(element => observer.observe(element));
    return () => observer.disconnect();
  }, [active]);

  const load = useCallback(async (refreshSteps = false) => {
    const current = ++request.current;
    setLoading(true);
    try {
      const response = await getWorkbench({ keyword: keyword || undefined, task_type: type || undefined, ...pagination });
      if (current !== request.current) return;
      setData(response);
      if (refreshSteps) setStepsRefreshVersion(version => version + 1);
      setLoadFailed(false);
    } catch {
      if (current !== request.current) return;
      // API errors are reported by the shared request interceptor.
      setLoadFailed(true);
    } finally {
      if (current === request.current) setLoading(false);
    }
  }, [keyword, type, pagination]);

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
    return () => {
      ++request.current;
      window.removeEventListener(WORKFLOW_TASKS_CHANGED, renamed);
      window.removeEventListener(CONVERSATION_TITLE_CHANGED_EVENT, renamed);
      window.removeEventListener(TASKS_REMOVED_EVENT, removed);
    };
  }, [active, load]);

  const globalSearch = keyword.trim().length > 0;
  const categoryVisible = (category: WorkbenchCategory) => globalSearch || activeCategory === category;
  const visibleCategories = CATEGORIES.filter(category => categoryVisible(category) && (data?.groups[category].total ?? 0) > 0);
  const count = (category: WorkbenchCategory) => data?.groups[category].total ?? 0;
  const openConversation = (id: string) => {
    selectChatConversationFilter('task');
    navigate(getChatConversationPath(id));
  };

  return (
    <div className='workbench'>
      <div className='task-metrics'>
        <Metric icon={<UserOutlined />} tone='orange' label={t('taskCenter.needsAttention')} value={count('attention')} active={activeCategory === 'attention'} onClick={() => setActiveCategory('attention')} />
        <Metric icon={<ClockCircleOutlined />} tone='blue' label={t('taskCenter.helpingYou')} value={count('running')} active={activeCategory === 'running'} onClick={() => setActiveCategory('running')} />
        <Metric icon={<PauseCircleOutlined />} tone='yellow' label={t('taskCenter.statusPaused')} value={count('paused')} active={activeCategory === 'paused'} onClick={() => setActiveCategory('paused')} />
        <Metric icon={<CloseCircleOutlined />} tone='red' label={t('taskCenter.statusFailed')} value={count('failed')} active={activeCategory === 'failed'} onClick={() => setActiveCategory('failed')} />
        <Metric icon={<CheckCircleFilled />} tone='green' label={t('taskCenter.completedLastSevenDays')} value={count('recent')} active={activeCategory === 'recent'} onClick={() => setActiveCategory('recent')} />
        <Metric icon={<StopOutlined />} tone='gray' label={t('taskCenter.statusCanceled')} value={count('canceled')} active={activeCategory === 'canceled'} onClick={() => setActiveCategory('canceled')} />
      </div>
      <div className='task-toolbar'>
        <Input prefix={<SearchOutlined />} allowClear placeholder={t('taskCenter.searchPlaceholder')} value={keyword} onChange={(event: React.ChangeEvent<HTMLInputElement>) => { setKeyword(event.target.value); setPagination(current => ({ page: 1, page_size: current.page_size })); }} />
        <Select value={type} onChange={(value) => { setType(value); setPagination(current => ({ page: 1, page_size: current.page_size })); }} options={[
          { value: '', label: t('taskCenter.triggerAll') },
          { value: 'workflow_run', label: t('taskCenter.typeWorkflowRun') },
          { value: 'background_chat', label: t('taskCenter.typeBackgroundChat') },
          { value: 'scheduled', label: t('taskCenter.typeScheduled') },
        ]} />
        <Button icon={<ReloadOutlined />} onClick={() => void load(true)}>{t('taskCenter.refresh')}</Button>
      </div>

      <div className='workbench-results' ref={resultsRef}><Spin spinning={loading}>
        {loadFailed ? <Alert className='workbench-load-error' type='warning' showIcon message={t('taskCenter.loadError')} description={t('taskCenter.loadErrorDescription')} action={<Button size='small' onClick={() => void load()}>{t('common.retry')}</Button>} /> : null}
        {data && visibleCategories.map(category => <WorkbenchSection key={category} category={category} group={data.groups[category]}
          pageSize={data.page_size} refreshVersion={stepsRefreshVersion} onPageChange={page => setPagination(current => ({ ...current, category, page }))}
          onSelect={setSelected} onOpenGraph={setGraphTask} />)}
        {!loading && !loadFailed && !visibleCategories.length ? <WorkbenchEmpty /> : null}
      </Spin></div>
      <TaskRemovalDialog task={removingTask} onClose={() => setRemovingTask(null)} />
      <TaskDetail task={selected} onClose={() => setSelected(null)} onOpenConversation={openConversation} onOpenGraph={(sessionId) => selected && setGraphTask({ ...selected, workflow_session_id: sessionId })} onDelete={setRemovingTask} />
      {graphTask?.workflow_session_id && <StateGraphModal open onClose={() => setGraphTask(null)} sessionId={graphTask.workflow_session_id} workflowId='' liveRefresh={false} fallbackSteps={graphTask.steps} />}
    </div>
  );
}

function Metric({ icon, tone, label, value, active, onClick }: { icon: React.ReactNode; tone: string; label: string; value: number; active: boolean; onClick: () => void }) {
  const buttonRef = useRef<HTMLButtonElement>(null);
  const iconRef = useRef<HTMLSpanElement>(null);
  const labelRef = useRef<HTMLSpanElement>(null);
  const [iconOnly, setIconOnly] = useState(false);

  useLayoutEffect(() => {
    const button = buttonRef.current!;
    const iconElement = iconRef.current!;
    const labelElement = labelRef.current!;
    const measure = () => {
      const style = getComputedStyle(button);
      const spacing = [style.paddingLeft, style.paddingRight, style.columnGap]
        .reduce((total, part) => total + (parseFloat(part) || 0), 0);
      // Hidden text keeps its intrinsic width so resizing can restore it without
      // toggling between the expanded and compact layouts on each observation.
      setIconOnly(iconElement.getBoundingClientRect().width + labelElement.scrollWidth + spacing > button.clientWidth);
    };
    measure();
    const observer = new ResizeObserver(measure);
    [button, iconElement, labelElement].forEach(element => observer.observe(element));
    return () => observer.disconnect();
  }, [label, active]);

  return <Tooltip title={iconOnly ? label : undefined}>
    <button ref={buttonRef} type='button' className={`task-metric ${tone}${active ? ' is-active' : ''}${iconOnly ? ' is-icon-only' : ''}`}
      aria-label={`${label} ${value}`} aria-pressed={active} onClick={onClick}>
      <span ref={iconRef} className='metric-icon-wrap'><span className={`metric-icon ${tone}`}>{icon}</span><span className={`metric-count-badge ${tone}`}>{value}</span></span>
      <span ref={labelRef} className='metric-label' aria-hidden={iconOnly}>{label}</span>
    </button>
  </Tooltip>;
}

function WorkbenchSection({ category, group, pageSize, refreshVersion, onPageChange, onSelect, onOpenGraph }: {
  category: WorkbenchCategory;
  group: WorkbenchResponse['groups'][WorkbenchCategory];
  pageSize: number;
  refreshVersion: number;
  onPageChange: (page: number) => void;
  onSelect: (task: Task) => void;
  onOpenGraph: (task: Task) => void;
}) {
  const { t } = useTranslation();
  const config = {
    attention: { icon: <UserOutlined />, title: 'needsAttention', description: 'needsAttentionDescription', action: 'taskCenter.confirmAction' },
    running: { icon: <SyncOutlined spin />, title: 'helpingYou', description: 'helpingYouDescription', action: 'taskCenter.viewAction' },
    paused: { icon: <PauseCircleOutlined />, title: 'statusPaused', description: 'pausedDescription', action: 'chat.workflowContinue' },
    failed: { icon: <CloseCircleOutlined />, title: 'statusFailed', description: 'failedDescription', action: 'taskCenter.viewAction' },
    recent: { icon: <CheckCircleFilled />, title: 'completedLastSevenDays', description: 'recentResultsDescription', action: 'taskCenter.viewAction' },
    canceled: { icon: <StopOutlined />, title: 'statusCanceled', description: 'canceledDescription', action: 'taskCenter.viewAction' },
  }[category];
  return <section className={`workbench-section ${category}`}>
    <header className='workbench-section-heading'>
      <div className='workbench-section-title'>
        <span className='workbench-section-icon-wrap'><span className={`workbench-section-icon ${category}`}>{config.icon}</span><span className={`section-count-badge ${category}`}>{group.total}</span></span>
        <div><h2>{t(`taskCenter.${config.title}`)}</h2><p>{t(`taskCenter.${config.description}`)}</p></div>
      </div>
    </header>
    <div className='attention-task-grid'>{group.items.map(task => <WorkbenchTaskCard key={task.id} task={task} tone={category}
      icon={config.icon} actionLabel={t(config.action)} onSelect={onSelect} onOpenGraph={onOpenGraph}
      refreshVersion={refreshVersion} showProgress={category === 'running'} />)}</div>
    {group.total > pageSize && <Pagination className='workbench-pagination' size='small' current={group.page} pageSize={pageSize}
      total={group.total} showSizeChanger={false} onChange={onPageChange} />}
  </section>;
}

function WorkbenchTaskCard({ task, tone, icon, actionLabel, onSelect, onOpenGraph, showProgress = false, refreshVersion = 0 }: {
  task: Task;
  tone: 'attention' | 'running' | 'paused' | 'failed' | 'recent' | 'canceled';
  icon: React.ReactNode;
  actionLabel: string;
  onSelect: (task: Task) => void;
  onOpenGraph?: (task: Task) => void;
  showProgress?: boolean;
  refreshVersion?: number;
}) {
  const { t } = useTranslation();
  const { steps, loading, failed } = useTaskSteps(showProgress ? task : null, refreshVersion);
  const progress = taskStepProgress(steps);
  return <article className={`attention-task-card workbench-task-card ${tone}`}>
    <div className='attention-task-card-top'>
      <span className={`task-type-icon status-icon-${tone}`}>{icon}</span>
      <div className='attention-task-card-title'>
        <Tooltip title={taskTitle(task, t)}><strong>{taskTitle(task, t)}</strong></Tooltip>
        <small>{taskMeta(task, t)}</small>
      </div>
      <StatusTag status={task.status} onClick={task.workflow_session_id && onOpenGraph ? () => onOpenGraph(task) : undefined} />
    </div>
    <p>{taskDescription(task, t)}</p>
    {showProgress && (loading || failed) ? <small>{t(failed ? 'taskCenter.loadError' : 'common.loading')}</small> : progress.total ? <div className='workbench-task-card-progress'><Progress percent={progress.percent} showInfo={false} size='small' /><small>{t('taskCenter.stepsCompleted', { done: progress.done, total: progress.total })}</small></div> : null}
    <footer><time>{formatDate(task.finished_at || task.updated_at)}</time><Button type='link' size='small' onClick={() => onSelect(task)}>{actionLabel} <RightOutlined /></Button></footer>
  </article>;
}

function WorkbenchEmpty() {
  const { t } = useTranslation();
  return <div className='workbench-empty'><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('taskCenter.empty')} /></div>;
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
