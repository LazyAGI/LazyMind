import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ScheduleList from './ScheduleList';

const mocks = vi.hoisted(() => ({
  listSchedules: vi.fn(), listScheduleTasks: vi.fn(), getPreferences: vi.fn(), getScheduleNotifications: vi.fn(),
}));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }));
vi.mock('./api', () => ({
  listSchedules: mocks.listSchedules, listScheduleTasks: mocks.listScheduleTasks,
  listAutomationGroups: vi.fn().mockResolvedValue({ items: [] }),
  batchCreateAutomationGroup: vi.fn(), cancelSchedule: vi.fn(), createSchedule: vi.fn(), deleteAutomationGroup: vi.fn(),
  deleteSchedule: vi.fn(), enableSchedule: vi.fn(), moveSchedule: vi.fn(), runScheduleNow: vi.fn(), updateSchedule: vi.fn(),
}));
vi.mock('@/modules/chat/utils/request', () => ({
  KnowledgeBaseServiceApi: () => ({ datasetServiceListDatasets: vi.fn().mockResolvedValue({ data: { datasets: [] } }) }),
}));
vi.mock('@/modules/chat/utils/chunkUpload', () => ({ uploadFileInChunks: vi.fn() }));
vi.mock('@/components/request', () => ({
  axiosInstance: { get: vi.fn().mockResolvedValue({ data: { data: { ready: true } } }) }, BASE_URL: '',
  getLocalizedErrorMessage: () => 'error', localizeErrorCode: (code: string) => code,
}));
vi.mock('@/modules/notifications/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/modules/notifications/api')>(),
  getPreferences: mocks.getPreferences, getScheduleNotifications: mocks.getScheduleNotifications,
}));
vi.mock('@/modules/notifications/RuleEditor', () => ({ default: () => <div>notification rules</div> }));
vi.mock('@/modules/notifications/NotificationSettings', () => ({ default: () => null }));

const schedule = {
  id: 'schedule-1', user_id: 'user-1', name: 'Daily report', prompt_template: 'Summarize sales', remark: '',
  cron_expr: '0 9 * * *', timezone: 'Asia/Shanghai', enabled: true, run_count: 0, group_position: 0,
  next_run_at: '2026-09-25T01:00:00Z', created_at: '2026-09-24T01:00:00Z',
};
beforeEach(() => {
  vi.clearAllMocks();
  mocks.listSchedules.mockResolvedValue({ items: [schedule] });
  mocks.listScheduleTasks.mockResolvedValue({ items: [], total: 0 });
  mocks.getScheduleNotifications.mockResolvedValue({ revision: 0, configured: false, config: null, availability: {} });
  mocks.getPreferences.mockResolvedValue({ revision: 1, enabled: true, defaults: { events: {}, channels: {} } });
});
afterEach(cleanup);

describe('schedule notification entry', () => {
  it('opens only the notification editor from a card and returns directly to the list', async () => {
    render(<ScheduleList active />);
    await screen.findByText(schedule.name);
    const configure = screen.getByRole('button', { name: 'notifications.configure' });
    fireEvent.click(configure);
    await screen.findByText('notification rules');
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(screen.queryByRole('heading', { name: 'taskCenter.scheduleDescription' })).not.toBeInTheDocument();
    expect(mocks.getScheduleNotifications).toHaveBeenCalledWith(schedule.id);
    expect(mocks.listScheduleTasks).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'notifications.cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(configure);
    await screen.findByText('notification rules');
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
  });

  it('keeps an explicitly opened schedule detail when its notification editor closes', async () => {
    render(<ScheduleList active />);
    fireEvent.click(await screen.findByText(schedule.name));
    await screen.findByRole('heading', { name: 'taskCenter.scheduleDescription' });
    const detail = screen.getByRole('dialog');
    await act(async () => { await Promise.resolve(); });
    fireEvent.click(within(detail).getByRole('button', { name: 'notifications.configure' }));
    await screen.findByText('notification rules');
    fireEvent.click(screen.getByRole('button', { name: 'notifications.cancel' }));
    await waitFor(() => expect(screen.getAllByRole('dialog')).toHaveLength(1));
    expect(screen.getByRole('heading', { name: 'taskCenter.scheduleDescription' })).toBeInTheDocument();
  });
});
