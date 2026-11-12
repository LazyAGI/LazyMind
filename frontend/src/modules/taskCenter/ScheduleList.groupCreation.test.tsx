import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ScheduleList from './ScheduleList';
import type { AutomationGroup, CreateScheduleRequest, Schedule } from './api';

const mocks = vi.hoisted(() => ({
  listSchedules: vi.fn(), listAutomationGroups: vi.fn(), createSchedule: vi.fn(),
  t: (key: string) => key,
}));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }));
vi.mock('./api', () => ({
  listSchedules: mocks.listSchedules, listAutomationGroups: mocks.listAutomationGroups, createSchedule: mocks.createSchedule,
  listScheduleTasks: vi.fn().mockResolvedValue({ items: [], total: 0 }),
  batchCreateAutomationGroup: vi.fn(), cancelSchedule: vi.fn(), deleteAutomationGroup: vi.fn(),
  deleteSchedule: vi.fn(), enableSchedule: vi.fn(), moveSchedule: vi.fn(), runScheduleNow: vi.fn(), updateSchedule: vi.fn(),
}));
vi.mock('@/modules/notifications/ScheduleNotificationPanel', () => ({ default: () => null }));
vi.mock('@/modules/chat/utils/request', () => ({
  KnowledgeBaseServiceApi: () => ({ datasetServiceListDatasets: vi.fn().mockResolvedValue({ data: { datasets: [] } }) }),
}));
vi.mock('@/modules/chat/utils/chunkUpload', () => ({ uploadFileInChunks: vi.fn() }));
vi.mock('@/components/request', () => ({
  axiosInstance: { get: vi.fn().mockResolvedValue({ data: { data: { ready: true } } }) }, BASE_URL: '',
  getLocalizedErrorMessage: () => 'error', localizeErrorCode: (code: string) => code,
}));

const groups: AutomationGroup[] = ['A', 'B'].map((name) => ({
  id: `group-${name}`, name: `Group ${name}`, remark: '', timezone: 'Asia/Shanghai', enabled: true,
  task_count: 1, created_at: '2026-10-01T00:00:00Z',
}));
const schedule: Schedule = {
  id: 'standalone', user_id: 'user-test', name: 'Standalone task', prompt_template: 'Prepare a report', remark: '',
  cron_expr: '0 9 * * *', timezone: 'Asia/Shanghai', enabled: true, run_count: 0, group_position: 0,
  next_run_at: '2026-10-20T01:00:00Z', created_at: '2026-10-01T00:00:00Z',
};
let schedules: Schedule[];

beforeEach(() => {
  vi.clearAllMocks();
  schedules = [schedule, ...groups.map((group) => ({
    ...schedule, id: `task-${group.id}`, name: `Existing ${group.name}`, group_id: group.id,
  }))];
  mocks.listSchedules.mockImplementation(async () => ({ items: [...schedules], total: schedules.length }));
  mocks.listAutomationGroups.mockResolvedValue({ items: groups, total: groups.length });
  mocks.createSchedule.mockImplementation(async (request: CreateScheduleRequest) => {
    const created = { ...schedule, ...request, id: 'created-task' };
    schedules = [created, ...schedules];
    return created;
  });
});
afterEach(cleanup);

function renderScheduleList() {
  render(<ConfigProvider theme={{ token: { motion: false } }}><ScheduleList active /></ConfigProvider>);
}

function groupCard(group: AutomationGroup) {
  return screen.getByRole('heading', { name: group.name }).closest('article')!;
}

async function openGroup(group: AutomationGroup) {
  await screen.findByRole('heading', { name: group.name });
  fireEvent.click(within(groupCard(group)).getByRole('button', { name: 'taskCenter.scheduleViewGroupTasks' }));
}

async function openCreate() {
  fireEvent.click(screen.getByRole('button', { name: /taskCenter\.newSchedule/ }));
  return screen.findByRole('dialog');
}

async function cancelCreate(dialog: HTMLElement) {
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
}

async function submitCreate(dialog: HTMLElement) {
  fireEvent.change(within(dialog).getByLabelText('taskCenter.scheduleNameInputLabel'), { target: { value: 'New report' } });
  fireEvent.change(within(dialog).getByLabelText('taskCenter.scheduleDescription'), { target: { value: 'Summarize the report' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'taskCenter.scheduleCreateBtn' }));
  await waitFor(() => expect(mocks.createSchedule).toHaveBeenCalledTimes(1));
}

describe('creating a task inside an existing group', () => {
  it.each(groups)('saves into $name and displays the new task there after refresh', async (group) => {
    renderScheduleList();
    await openGroup(group);
    const dialog = await openCreate();
    await submitCreate(dialog);

    expect(mocks.createSchedule).toHaveBeenCalledWith(expect.objectContaining({ group_id: group.id }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(await screen.findByText('New report')).toBeVisible();
    expect(screen.getByText(`Existing ${group.name}`)).toBeVisible();
    expect(screen.queryByText('Standalone task')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('taskCenter.scheduleGroupedView'));
    expect(within(groupCard(group)).getByText('New report')).toBeVisible();
    const otherTasks = screen.getByRole('heading', { name: 'taskCenter.scheduleOtherTasks' }).closest('section')!;
    expect(within(otherTasks).queryByText('New report')).not.toBeInTheDocument();
  });

  it('uses the newly opened group after canceling a draft in another group', async () => {
    renderScheduleList();
    await openGroup(groups[0]);
    await cancelCreate(await openCreate());
    fireEvent.click(screen.getByText('taskCenter.scheduleGroupedView'));
    await openGroup(groups[1]);
    const dialog = await openCreate();

    await waitFor(() => expect(within(dialog).getByText(groups[1].name)).toBeVisible());
    expect(within(dialog).queryByText(groups[0].name)).not.toBeInTheDocument();
    await submitCreate(dialog);
    expect(mocks.createSchedule).toHaveBeenCalledWith(expect.objectContaining({ group_id: groups[1].id }));
  });

  it('reapplies the current group after canceling and reopening creation', async () => {
    renderScheduleList();
    await openGroup(groups[0]);
    await cancelCreate(await openCreate());
    const dialog = await openCreate();

    await waitFor(() => expect(within(dialog).getByText(groups[0].name)).toBeVisible());
    await submitCreate(dialog);
    expect(mocks.createSchedule).toHaveBeenCalledWith(expect.objectContaining({ group_id: groups[0].id }));
  });

  it('does not retain the group when creating from the global task list', async () => {
    renderScheduleList();
    await openGroup(groups[0]);
    await cancelCreate(await openCreate());
    fireEvent.click(screen.getByRole('button', { name: 'taskCenter.viewAll' }));
    const dialog = await openCreate();

    expect(within(dialog).queryByText(groups[0].name)).not.toBeInTheDocument();
    await submitCreate(dialog);
    expect(mocks.createSchedule.mock.calls[0][0].group_id).toBeUndefined();
  });

  it('submits a manually selected group instead of overriding it with the current view', async () => {
    renderScheduleList();
    await openGroup(groups[0]);
    const dialog = await openCreate();
    const groupSelect = within(dialog).getByLabelText('taskCenter.scheduleJoinGroup');
    fireEvent.mouseDown(groupSelect);
    await screen.findByRole('listbox');
    fireEvent.keyDown(groupSelect, { key: 'ArrowDown', code: 'ArrowDown', keyCode: 40 });
    fireEvent.keyDown(groupSelect, { key: 'Enter', code: 'Enter', keyCode: 13 });
    await waitFor(() => expect(within(dialog).getByText(groups[1].name)).toBeVisible());
    await submitCreate(dialog);

    expect(mocks.createSchedule).toHaveBeenCalledWith(expect.objectContaining({ group_id: groups[1].id }));
  });
});
