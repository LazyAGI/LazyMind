import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import ScheduleNotificationPanel from './ScheduleNotificationPanel';
import { emptyRule } from './api';
const mocks = vi.hoisted(() => ({ prefs: vi.fn(), schedule: vi.fn(), put: vi.fn(), runs: vi.fn(), accounts: vi.fn(), execution: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async original => ({ ...await original<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts }));
vi.mock('@/modules/taskCenter/api', () => ({ listScheduleTasks: mocks.runs }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), getPreferences: mocks.prefs, getScheduleNotifications: mocks.schedule, putScheduleNotifications: mocks.put, getExecutionNotifications: mocks.execution }));
vi.mock('react-i18next', async original => ({ ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/runtime/mode', async original => ({ ...await original<typeof import('@/runtime/mode')>(), isDesktopRuntime: () => true }));
beforeEach(() => {
 vi.clearAllMocks();
 const config = emptyRule(); config.channels.desktop = { enabled: true };
 mocks.prefs.mockResolvedValue({ revision: 1, enabled: true, defaults: config });
 mocks.schedule.mockResolvedValue({ revision: 3, configured: true, config, availability: {} });
 mocks.accounts.mockResolvedValue({ items: [] }); mocks.runs.mockResolvedValue({ items: [], total: 0 });
});
afterEach(async () => { cleanup(); vi.restoreAllMocks(); });
const mount = (props: React.ComponentProps<typeof ScheduleNotificationPanel>) => render(<MemoryRouter><ScheduleNotificationPanel compact draftMode {...props} /></MemoryRouter>);
describe('schedule notification drafts', () => {
 it('stages a new task override without writing a schedule or standalone notification', async () => {
  const changed = vi.fn(); mount({ onDraftChange: changed });
  const open = await screen.findByRole('button', { name: 'notifications.configure' });
  await waitFor(() => expect(open).toBeEnabled()); fireEvent.click(open);
  fireEvent.click(await screen.findByRole('switch', { name: 'notifications.waiting' }));
  fireEvent.click(screen.getByRole('button', { name: 'notifications.save' }));
  await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ events: expect.objectContaining({ waiting: { enabled: true, content: 'summary' } }) }) })));
  expect(mocks.put).not.toHaveBeenCalled(); expect(mocks.schedule).not.toHaveBeenCalled();
 });
});
