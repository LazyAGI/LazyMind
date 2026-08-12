import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Empty, Modal, Skeleton, Switch, Tag, message } from 'antd';
import { CalendarOutlined, ReloadOutlined, RightOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { cancelSchedule, enableSchedule, listSchedules } from './api';
import type { Schedule } from './api';
import { describeCron } from './ScheduleList';

interface SettingsScheduleListProps {
  masterEnabled: boolean;
  onChanged?: () => void | Promise<void>;
}

function scheduleName(schedule: Schedule) {
  return schedule.name?.trim() || schedule.prompt_template.trim() || '未命名定时任务';
}

export default function SettingsScheduleList({ masterEnabled, onChanged }: SettingsScheduleListProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [updatingID, setUpdatingID] = useState<string | null>(null);

  const loadSchedules = useCallback(async () => {
    setLoading(true);
    setLoadError(false);
    try {
      const response = await listSchedules(true);
      setSchedules(response.items ?? []);
    } catch {
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadSchedules();
  }, [loadSchedules]);

  const enabledCount = schedules.filter((schedule) => schedule.enabled).length;

  const applyScheduleState = async (schedule: Schedule, enabled: boolean) => {
    if (updatingID) return;
    setUpdatingID(schedule.id);
    setSchedules((items) => items.map((item) => item.id === schedule.id ? { ...item, enabled } : item));
    try {
      let updated: Schedule | undefined;
      if (enabled) {
        updated = await enableSchedule(schedule.id);
      } else {
        await cancelSchedule(schedule.id);
      }
      if (updated) {
        setSchedules((items) => items.map((item) => item.id === schedule.id ? updated : item));
      }
      message.success(enabled ? '定时任务已启用' : '定时任务已停用');
      await onChanged?.();
    } catch {
      setSchedules((items) => items.map((item) => item.id === schedule.id ? schedule : item));
      message.error('更新失败，已恢复原状态');
    } finally {
      setUpdatingID(null);
    }
  };

  const requestScheduleState = (schedule: Schedule, enabled: boolean) => {
    if (enabled) {
      void applyScheduleState(schedule, true);
      return;
    }
    const downstream = schedules.filter((candidate) => candidate.dependencies?.some((dependency) => dependency.source_schedule_id === schedule.id));
    if (!downstream.length) {
      void applyScheduleState(schedule, false);
      return;
    }
    Modal.confirm({
      title: `停用“${scheduleName(schedule)}”？`,
      content: <div className="settings-ref-confirm"><p>该任务停用后不会再产生新的调度。</p><p>以下任务依赖它：{downstream.map(scheduleName).join('、')}。</p><p>任务配置和原始开关会被保留，正在执行的内容不会被强制终止。</p></div>,
      okText: '确认停用',
      cancelText: '取消',
      okButtonProps: { danger: true },
      onOk: () => applyScheduleState(schedule, false),
    });
  };

  return <section className="settings-schedule-panel" aria-busy={loading}>
    <header className="settings-schedule-heading">
      <div><h2>定时任务</h2><p>{enabledCount} / {schedules.length} 已启用</p></div>
      <button type="button" onClick={() => navigate('/task-center?tab=schedules')} aria-label="查看全部定时任务详情">查看详情<RightOutlined /></button>
    </header>
    {loading ? <div className="settings-schedule-loading"><Skeleton active paragraph={{ rows: 3 }} /></div> : null}
    {!loading && loadError ? <Alert type="error" showIcon message="无法加载定时任务" description="请检查连接后重试。" action={<Button size="small" icon={<ReloadOutlined />} onClick={() => void loadSchedules()}>重试</Button>} /> : null}
    {!loading && !loadError && !schedules.length ? <div className="settings-schedule-empty"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无定时任务" /></div> : null}
    {!loading && !loadError && schedules.length ? <div className="settings-schedule-list">
      {schedules.map((schedule) => {
        const effectiveEnabled = masterEnabled && schedule.enabled;
        const statusText = !masterEnabled && schedule.enabled ? '随任务中心暂停' : effectiveEnabled ? '运行中' : '已停用';
        return <article className={`settings-schedule-row${effectiveEnabled ? '' : ' is-paused'}`} key={schedule.id}>
          <span className="settings-schedule-icon" aria-hidden="true"><CalendarOutlined /></span>
          <div className="settings-schedule-copy">
            <h3>{scheduleName(schedule)}</h3>
            <p>{describeCron(schedule.cron_expr, (key) => t(key))}{effectiveEnabled && schedule.next_run_at ? `，下次运行：${dayjs(schedule.next_run_at).format('M 月 D 日 HH:mm')}` : '，已暂停'}</p>
          </div>
          <Tag className={`settings-schedule-status ${effectiveEnabled ? 'is-running' : !masterEnabled && schedule.enabled ? 'is-suspended' : 'is-disabled'}`}>{statusText}</Tag>
          <Switch
            className="settings-ref-switch"
            checked={schedule.enabled}
            loading={updatingID === schedule.id}
            disabled={!masterEnabled || Boolean(updatingID)}
            onChange={(checked) => requestScheduleState(schedule, checked)}
            aria-label={`${scheduleName(schedule)}启用状态`}
          />
        </article>;
      })}
    </div> : null}
    <div className="settings-screenreader-status" role="status" aria-live="polite">{updatingID ? '正在更新定时任务' : ''}</div>
  </section>;
}
