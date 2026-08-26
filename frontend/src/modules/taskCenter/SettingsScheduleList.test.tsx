import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import SettingsScheduleList from './SettingsScheduleList';

const mocks = vi.hoisted(() => ({
  cancelSchedule: vi.fn(),
  enableSchedule: vi.fn(),
  listSchedules: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock('antd', async () => {
  const actual = await vi.importActual<typeof import('antd')>('antd');
  return {
    ...actual,
    message: { error: vi.fn(), success: vi.fn() },
  };
});

vi.mock('react-i18next', async () => {
  const actual = await vi.importActual<typeof import('react-i18next')>('react-i18next');
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string, values?: Record<string, unknown>) => {
        if (key === 'settingsPage.tasks.pausedWithSchedules') return '随定时任务开关暂停';
        if (key === 'settingsPage.tasks.enableAria') return `${values?.name}启用状态`;
        if (key === 'settingsPage.tasks.scheduleEnabledCount') return `${values?.enabled} / ${values?.total} 已启用`;
        return key;
      },
    }),
  };
});

vi.mock('react-router-dom', () => ({
  useNavigate: () => mocks.navigate,
}));

vi.mock('./api', () => ({
  cancelSchedule: mocks.cancelSchedule,
  enableSchedule: mocks.enableSchedule,
  listSchedules: mocks.listSchedules,
}));

const schedule = {
  id: 'schedule-1',
  user_id: 'user-1',
  name: '日报',
  remark: '',
  cron_expr: '0 9 * * *',
  timezone: 'Asia/Shanghai',
  prompt_template: '生成日报',
  group_position: 0,
  enabled: true,
  run_count: 0,
  next_run_at: '2026-08-26T01:00:00Z',
  created_at: '2026-08-25T01:00:00Z',
};

describe('SettingsScheduleList', () => {
  beforeEach(() => {
    mocks.cancelSchedule.mockReset();
    mocks.cancelSchedule.mockResolvedValue(undefined);
    mocks.enableSchedule.mockReset();
    mocks.listSchedules.mockReset();
    mocks.listSchedules.mockResolvedValue({ items: [schedule], total: 1 });
    mocks.navigate.mockReset();
  });

  it('keeps schedule rows paused and disabled when scheduled tasks are off', async () => {
    render(<SettingsScheduleList schedulesEnabled={false} />);

    expect(await screen.findByText('随定时任务开关暂停')).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: '日报启用状态' })).toBeDisabled();
    expect(mocks.cancelSchedule).not.toHaveBeenCalled();
  });

  it('allows a schedule to be changed when scheduled tasks are on', async () => {
    const onChanged = vi.fn();
    render(<SettingsScheduleList schedulesEnabled onChanged={onChanged} />);

    fireEvent.click(await screen.findByRole('switch', { name: '日报启用状态' }));

    await waitFor(() => expect(mocks.cancelSchedule).toHaveBeenCalledWith('schedule-1'));
    expect(onChanged).toHaveBeenCalledOnce();
  });
});
