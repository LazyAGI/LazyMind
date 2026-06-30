import { Tabs } from 'antd';
import { useTranslation } from 'react-i18next';
import TaskList from './TaskList';
import ScheduleList from './ScheduleList';
import './index.scss';

export default function TaskCenterPage() {
  const { t } = useTranslation();

  return (
    <div className='task-center-page'>
      <Tabs
        defaultActiveKey='tasks'
        items={[
          {
            key: 'tasks',
            label: t('taskCenter.tasks'),
            children: <TaskList />,
          },
          {
            key: 'schedules',
            label: t('taskCenter.schedules'),
            children: <ScheduleList />,
          },
        ]}
      />
    </div>
  );
}
