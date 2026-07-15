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

  return (
    <div className='task-center-page'>
      <header className='task-center-header'><h1>{t('taskCenter.title')}</h1></header>
      <Tabs className='task-center-tabs' activeKey={activeTab} onChange={(key) => setActiveTab(key as TaskCenterTab)} items={[
        { key: 'workbench', label: t('taskCenter.workbench'), children: <Workbench active={activeTab === 'workbench'} /> },
        { key: 'tasks', label: t('taskCenter.allTasks'), children: <TaskList active={activeTab === 'tasks'} /> },
        { key: 'schedules', label: t('taskCenter.schedulePlans'), children: <ScheduleList active={activeTab === 'schedules'} /> },
      ]} />
    </div>
  );
}
