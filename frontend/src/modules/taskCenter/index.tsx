import { Tabs } from 'antd';
import { useTranslation } from 'react-i18next';
import Workbench from './Workbench';
import TaskList from './TaskList';
import ScheduleList from './ScheduleList';
import './index.scss';

export default function TaskCenterPage() {
  const { t } = useTranslation();
  return (
    <div className='task-center-page'>
      <header className='task-center-header'><h1>{t('taskCenter.title')}</h1></header>
      <Tabs className='task-center-tabs' defaultActiveKey='workbench' items={[
        { key: 'workbench', label: t('taskCenter.workbench'), children: <Workbench /> },
        { key: 'tasks', label: t('taskCenter.allTasks'), children: <TaskList /> },
        { key: 'schedules', label: t('taskCenter.schedulePlans'), children: <ScheduleList /> },
      ]} />
    </div>
  );
}
