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
  schedulesEnabled: boolean;
  onChanged?: () => void | Promise<void>;
}

export default function SettingsScheduleList({ schedulesEnabled, onChanged }: SettingsScheduleListProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [updatingID, setUpdatingID] = useState<string | null>(null);

  const scheduleName = useCallback((schedule: Schedule) => {
    return schedule.name?.trim() || schedule.prompt_template.trim() || t('settingsPage.tasks.unnamed');
  }, [t]);

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
      message.success(enabled ? t('settingsPage.tasks.enabledToast') : t('settingsPage.tasks.disabledToast'));
      await onChanged?.();
    } catch {
      setSchedules((items) => items.map((item) => item.id === schedule.id ? schedule : item));
      message.error(t('settingsPage.tasks.updateFailed'));
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
      title: t('settingsPage.tasks.disableTitle', { name: scheduleName(schedule) }),
      content: <div className="settings-ref-confirm">
        <p>{t('settingsPage.tasks.disableContentNoNew')}</p>
        <p>{t('settingsPage.tasks.disableContentDeps', { names: downstream.map(scheduleName).join(t('settingsPage.tasks.listSeparator')) })}</p>
        <p>{t('settingsPage.tasks.disableContentKeep')}</p>
      </div>,
      okText: t('settingsPage.tasks.confirmDisable'),
      cancelText: t('settingsPage.cancel'),
      okButtonProps: { danger: true },
      onOk: () => applyScheduleState(schedule, false),
    });
  };

  return <section className="settings-schedule-panel" aria-busy={loading}>
    <header className="settings-schedule-heading">
      <div>
        <h2>{t('settingsPage.tasks.scheduleTitle')}</h2>
        <p>{t('settingsPage.tasks.scheduleEnabledCount', { enabled: enabledCount, total: schedules.length })}</p>
      </div>
      <button type="button" onClick={() => navigate('/task-center?tab=schedules')} aria-label={t('settingsPage.tasks.viewDetailsAria')}>
        {t('settingsPage.tasks.viewDetails')}<RightOutlined />
      </button>
    </header>
    {loading ? <div className="settings-schedule-loading"><Skeleton active paragraph={{ rows: 3 }} /></div> : null}
    {!loading && loadError ? <Alert type="error" showIcon message={t('settingsPage.tasks.loadFailed')} description={t('settingsPage.tasks.loadFailedDesc')} action={<Button size="small" icon={<ReloadOutlined />} onClick={() => void loadSchedules()}>{t('settingsPage.retry')}</Button>} /> : null}
    {!loading && !loadError && !schedules.length ? <div className="settings-schedule-empty"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('settingsPage.tasks.empty')} /></div> : null}
    {!loading && !loadError && schedules.length ? <div className="settings-schedule-list">
      {schedules.map((schedule) => {
        const effectiveEnabled = schedulesEnabled && schedule.enabled;
        const statusText = !schedulesEnabled && schedule.enabled
          ? t('settingsPage.tasks.pausedWithSchedules')
          : effectiveEnabled
            ? t('settingsPage.running')
            : t('settingsPage.disabled');
        const nextRunText = effectiveEnabled && schedule.next_run_at
          ? t('settingsPage.tasks.nextRun', {
            time: dayjs(schedule.next_run_at).format(t('settingsPage.tasks.nextRunFormat')),
          })
          : t('settingsPage.tasks.pausedSuffix');
        return <article className={`settings-schedule-row${effectiveEnabled ? '' : ' is-paused'}`} key={schedule.id}>
          <span className="settings-schedule-icon" aria-hidden="true"><CalendarOutlined /></span>
          <div className="settings-schedule-copy">
            <h3>{scheduleName(schedule)}</h3>
            <p>{describeCron(schedule.cron_expr, (key) => t(key))}{nextRunText}</p>
          </div>
          <Tag className={`settings-schedule-status ${effectiveEnabled ? 'is-running' : !schedulesEnabled && schedule.enabled ? 'is-suspended' : 'is-disabled'}`}>{statusText}</Tag>
          <Switch
            className="settings-ref-switch"
            checked={schedule.enabled}
            loading={updatingID === schedule.id}
            disabled={!schedulesEnabled || Boolean(updatingID)}
            onChange={(checked: boolean) => requestScheduleState(schedule, checked)}
            aria-label={t('settingsPage.tasks.enableAria', { name: scheduleName(schedule) })}
          />
        </article>;
      })}
    </div> : null}
    <div className="settings-screenreader-status" role="status" aria-live="polite">{updatingID ? t('settingsPage.tasks.updatingStatus') : ''}</div>
  </section>;
}
