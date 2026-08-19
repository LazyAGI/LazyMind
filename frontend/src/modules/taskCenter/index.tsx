import { useEffect, useState } from 'react';
import { Tabs } from 'antd';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import Workbench from './Workbench';
import TaskList from './TaskList';
import ScheduleList from './ScheduleList';
import './index.scss';

type TaskCenterTab = 'workbench' | 'tasks' | 'schedules';

function parseTaskCenterTab(value: string | null): TaskCenterTab {
  return value === 'tasks' || value === 'schedules' ? value : 'workbench';
}

interface TaskCenterPageProps {
  embedded?: boolean;
}

export default function TaskCenterPage({ embedded = false }: TaskCenterPageProps) {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedTab = parseTaskCenterTab(searchParams.get('tab'));
  const [activeTab, setActiveTab] = useState<TaskCenterTab>(requestedTab);
  const [taskStatus, setTaskStatus] = useState('');
  const [taskPage, setTaskPage] = useState(1);

  useEffect(() => {
    setActiveTab(requestedTab);
  }, [requestedTab]);

  const selectTab = (tab: TaskCenterTab) => {
    const nextParams = new URLSearchParams(searchParams);
    nextParams.set('tab', tab);
    setSearchParams(nextParams, { replace: true });
    setActiveTab(tab);
  };

  const showTasksByStatus = (status: 'failed' | 'canceled') => {
    setTaskStatus(status);
    setTaskPage(1);
    selectTab('tasks');
  };

  return (
    <div className={`task-center-page${embedded ? ' is-embedded' : ''}`}>
      {!embedded ? <header className='task-center-header'>
        <div className='task-center-title-line'>
          <div>
            <h1>{t('taskCenter.title')}</h1>
            <p>{t('taskCenter.description')}</p>
          </div>
        </div>
      </header> : null}
      <Tabs className='task-center-tabs' activeKey={activeTab} onChange={(key: string) => selectTab(key as TaskCenterTab)} items={[
        { key: 'workbench', label: t('taskCenter.workbench'), children: <Workbench active={activeTab === 'workbench'} onViewAllStatus={showTasksByStatus} /> },
        { key: 'tasks', label: t('taskCenter.allTasks'), children: <TaskList active={activeTab === 'tasks'} status={taskStatus} onStatusChange={setTaskStatus} page={taskPage} onPageChange={setTaskPage} /> },
        { key: 'schedules', label: t('taskCenter.schedulePlans'), children: <ScheduleList active={activeTab === 'schedules'} /> },
      ]} />
    </div>
  );
}
