import { useState } from 'react';
import { Tabs } from 'antd';
import { useTranslation } from 'react-i18next';
import Workbench from './Workbench';
import TaskList from './TaskList';
import ScheduleList from './ScheduleList';
import './index.scss';

type TaskCenterTab = 'workbench' | 'tasks' | 'schedules';

export default function TaskCenterPage() {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState<TaskCenterTab>('workbench');
  const [taskStatus, setTaskStatus] = useState('');
  const [taskPage, setTaskPage] = useState(1);

  const showTasksByStatus = (status: 'failed' | 'canceled') => {
    setTaskStatus(status);
    setTaskPage(1);
    setActiveTab('tasks');
  };

  return (
    <div className='task-center-page'>
      <header className='task-center-header'>
        <div className='task-center-title-line'>
          <div>
            <h1>{t('taskCenter.title')}</h1>
            <p>{t('taskCenter.description')}</p>
          </div>
        </div>
      </header>
      <Tabs className='task-center-tabs' activeKey={activeTab} onChange={(key: string) => setActiveTab(key as TaskCenterTab)} items={[
        { key: 'workbench', label: t('taskCenter.workbench'), children: <Workbench active={activeTab === 'workbench'} onViewAllStatus={showTasksByStatus} /> },
        { key: 'tasks', label: t('taskCenter.allTasks'), children: <TaskList active={activeTab === 'tasks'} status={taskStatus} onStatusChange={setTaskStatus} page={taskPage} onPageChange={setTaskPage} /> },
        { key: 'schedules', label: t('taskCenter.schedulePlans'), children: <ScheduleList active={activeTab === 'schedules'} /> },
      ]} />
    </div>
  );
}
