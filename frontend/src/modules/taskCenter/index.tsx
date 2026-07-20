import { useState } from 'react';
import { Button, Tabs } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import Workbench from './Workbench';
import TaskList from './TaskList';
import ScheduleList from './ScheduleList';
import { CHAT_NEW_RUN_IN_BACKGROUND_KEY, CHAT_RESUME_CONVERSATION_KEY } from '@/modules/chat/constants/chat';
import './index.scss';

type TaskCenterTab = 'workbench' | 'tasks' | 'schedules';

export default function TaskCenterPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState<TaskCenterTab>('workbench');

  const handleNewTask = () => {
    sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
    sessionStorage.setItem(CHAT_NEW_RUN_IN_BACKGROUND_KEY, '1');
    navigate('/agent/chat/home');
  };

  return (
    <div className='task-center-page'>
      <header className='task-center-header'>
        <div className='task-center-title-line'>
          <div>
            <h1>{t('taskCenter.title')}</h1>
            <p>{t('taskCenter.description')}</p>
          </div>
          <Button type='primary' icon={<PlusOutlined />} onClick={handleNewTask}>
            {t('taskCenter.newTask')}
          </Button>
        </div>
      </header>
      <Tabs className='task-center-tabs' activeKey={activeTab} onChange={(key: string) => setActiveTab(key as TaskCenterTab)} items={[
        { key: 'workbench', label: t('taskCenter.workbench'), children: <Workbench active={activeTab === 'workbench'} /> },
        { key: 'tasks', label: t('taskCenter.allTasks'), children: <TaskList active={activeTab === 'tasks'} /> },
        { key: 'schedules', label: t('taskCenter.schedulePlans'), children: <ScheduleList active={activeTab === 'schedules'} /> },
      ]} />
    </div>
  );
}
