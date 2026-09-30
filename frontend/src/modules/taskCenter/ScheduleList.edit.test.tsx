import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import dayjs from 'dayjs';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ScheduleList from './ScheduleList';
import type { Schedule } from './api';

const mocks = vi.hoisted(() => ({
  listSchedules: vi.fn(), listScheduleTasks: vi.fn(), updateSchedule: vi.fn(),
  t: (key: string, values?: { time?: string }) => values?.time ? `${key} ${values.time}` : key,
}));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }));
vi.mock('./api', () => ({
  listSchedules: mocks.listSchedules, listScheduleTasks: mocks.listScheduleTasks, updateSchedule: mocks.updateSchedule,
  listAutomationGroups: vi.fn().mockResolvedValue({ items: [] }),
  batchCreateAutomationGroup: vi.fn(), cancelSchedule: vi.fn(), createSchedule: vi.fn(), deleteAutomationGroup: vi.fn(),
  deleteSchedule: vi.fn(), enableSchedule: vi.fn(), moveSchedule: vi.fn(), runScheduleNow: vi.fn(),
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

const schedule: Schedule = {
  id: 'schedule-1', user_id: 'user-1', name: 'Daily report', prompt_template: 'Summarize sales', remark: '',
  cron_expr: '39 10 * * 1,2,3,4,5', timezone: 'Asia/Shanghai', enabled: true, run_count: 0, group_position: 0,
  next_run_at: '2026-10-01T10:39:00+08:00', created_at: '2026-09-30T09:00:00+08:00',
};
const updatedSchedule: Schedule = {
  ...schedule, name: 'Updated report', prompt_template: 'Summarize updated sales',
  cron_expr: '7 11 * * 1,2,3,4,5', next_run_at: '2026-10-01T11:07:00+08:00',
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.listSchedules.mockResolvedValue({ items: [schedule] });
  mocks.listScheduleTasks.mockResolvedValue({ items: [], total: 0 });
  mocks.updateSchedule.mockResolvedValue(updatedSchedule);
});
afterEach(cleanup);

async function openDetailEditor() {
  fireEvent.click(await screen.findByText(schedule.name));
  const detail = await screen.findByRole('dialog');
  fireEvent.click(within(detail).getByRole('button', { name: 'taskCenter.scheduleEdit' }));
  const editor = (await screen.findByText('notifications.editSchedule')).closest<HTMLElement>('[role="dialog"]')!;
  return { detail, editor };
}

function timeInput(editor: HTMLElement) {
  return editor.querySelector<HTMLInputElement>('.ant-picker-input input')!;
}

function editSchedule(editor: HTMLElement) {
  fireEvent.change(within(editor).getByLabelText('taskCenter.scheduleNameInputLabel'), { target: { value: updatedSchedule.name } });
  fireEvent.change(within(editor).getByLabelText('taskCenter.scheduleDescription'), { target: { value: updatedSchedule.prompt_template } });
  fireEvent.change(timeInput(editor), { target: { value: '11:07' } });
  fireEvent.keyDown(timeInput(editor), { key: 'Enter', code: 'Enter' });
  fireEvent.click(within(editor).getByRole('button', { name: 'taskCenter.scheduleSaveBtn' }));
}

async function expectUpdatedDetail(detail: HTMLElement) {
  await waitFor(() => expect(within(detail).getByText(updatedSchedule.name)).toBeInTheDocument());
  expect(within(detail).getByText(updatedSchedule.prompt_template)).toBeInTheDocument();
  expect(within(detail).getByText(/11:07.*Asia\/Shanghai/)).toBeInTheDocument();
  expect(within(detail).getByText(dayjs(updatedSchedule.next_run_at).format('YYYY/MM/DD HH:mm:ss'))).toBeInTheDocument();
  fireEvent.click(within(detail).getByRole('button', { name: 'taskCenter.scheduleEdit' }));
  const editor = (await screen.findByText('notifications.editSchedule')).closest<HTMLElement>('[role="dialog"]')!;
  expect(timeInput(editor)).toHaveValue('11:07');
  expect(within(editor).getByLabelText('taskCenter.scheduleNameInputLabel')).toHaveValue(updatedSchedule.name);
  expect(within(editor).getByLabelText('taskCenter.scheduleDescription')).toHaveValue(updatedSchedule.prompt_template);
  return editor;
}

describe('schedule detail editing', () => {
  it.each(['save', 'cancel'])('keeps the edited time and title throughout the closing animation after %s', async (action) => {
    render(<ScheduleList active />);
    const { editor } = await openDetailEditor();
    if (action === 'save') {
      mocks.listSchedules.mockResolvedValue({ items: [updatedSchedule] });
      editSchedule(editor);
      await waitFor(() => expect(mocks.listSchedules).toHaveBeenCalledTimes(2));
    } else {
      fireEvent.change(timeInput(editor), { target: { value: '11:07' } });
      fireEvent.keyDown(timeInput(editor), { key: 'Enter', code: 'Enter' });
      fireEvent.click(within(editor).getByRole('button', { name: 'Cancel' }));
      expect(mocks.updateSchedule).not.toHaveBeenCalled();
    }

    await waitFor(() => expect(editor).toHaveClass('ant-zoom-leave'));
    expect(timeInput(editor)).toHaveValue('11:07');
    expect(within(editor).getByText('notifications.editSchedule')).toBeInTheDocument();
    expect(within(editor).getByLabelText('taskCenter.scheduleNameInputLabel')).toHaveValue(action === 'save' ? updatedSchedule.name : schedule.name);
  });

  it.each([
    ['weekly', schedule.cron_expr, '7 11 * * 1,2,3,4,5'],
    ['monthly cadence', '@every:2:month;39 10 1,15 * *', '@every:2:month;7 11 1,15 * *'],
  ])('saves a %s time selected from the panel without a second confirmation', async (_, cronExpr, expectedCronExpr) => {
    mocks.listSchedules.mockResolvedValue({ items: [{ ...schedule, cron_expr: cronExpr }] });
    render(<ScheduleList active />);
    const { editor } = await openDetailEditor();
    fireEvent.focus(timeInput(editor));
    fireEvent.click(timeInput(editor));
    const panel = document.querySelector<HTMLElement>('.ant-picker-dropdown')!;
    const columns = within(panel).getAllByRole('list');
    fireEvent.click(within(columns[0]).getByText('11', { exact: true }));
    fireEvent.click(within(columns[1]).getByText('07', { exact: true }));
    const saveButton = within(editor).getByRole('button', { name: 'taskCenter.scheduleSaveBtn' });
    fireEvent.mouseDown(saveButton);
    fireEvent.blur(timeInput(editor));
    fireEvent.click(saveButton);

    await waitFor(() => expect(mocks.updateSchedule).toHaveBeenCalledTimes(1));
    expect(mocks.updateSchedule).toHaveBeenCalledWith(schedule.id, expect.objectContaining({ cron_expr: expectedCronExpr }));
  });

  it('refreshes the open detail and uses the saved fields when editing again', async () => {
    render(<ScheduleList active />);
    const { detail, editor } = await openDetailEditor();
    mocks.listSchedules.mockResolvedValue({ items: [updatedSchedule] });
    editSchedule(editor);

    await waitFor(() => expect(mocks.listSchedules).toHaveBeenCalledTimes(2));
    expect(mocks.updateSchedule).toHaveBeenCalledWith(schedule.id, expect.objectContaining({
      name: updatedSchedule.name, prompt_template: updatedSchedule.prompt_template, cron_expr: updatedSchedule.cron_expr,
    }));
    await expectUpdatedDetail(detail);
  });

  it('uses the saved response immediately while the list refresh is pending', async () => {
    render(<ScheduleList active />);
    const { detail, editor } = await openDetailEditor();
    mocks.listSchedules.mockImplementation(() => new Promise(() => {}));
    editSchedule(editor);

    await waitFor(() => expect(mocks.listSchedules).toHaveBeenCalledTimes(2));
    await expectUpdatedDetail(detail);
  });

  it('preserves saved dependencies when the update response omits them and the list refresh fails', async () => {
    const source = { ...schedule, id: 'source-schedule', name: 'Source report', cron_expr: '0 9 * * *' };
    mocks.listSchedules.mockResolvedValue({ items: [{ ...schedule, dependencies: [{ source_schedule_id: source.id }] }, source] });
    mocks.updateSchedule.mockResolvedValue({ ...updatedSchedule, dependencies: null });
    render(<ScheduleList active />);
    const { detail, editor } = await openDetailEditor();
    mocks.listSchedules.mockRejectedValue(new Error('refresh failed'));
    editSchedule(editor);

    await waitFor(() => expect(mocks.listSchedules).toHaveBeenCalledTimes(2));
    const reopenedEditor = await expectUpdatedDetail(detail);
    expect(within(reopenedEditor).getByText(new RegExp(source.name))).toBeInTheDocument();
    const saveButton = within(reopenedEditor).getByText('taskCenter.scheduleSaveBtn').closest('button')!;
    await waitFor(() => expect(saveButton).not.toHaveClass('ant-btn-loading'));
    fireEvent.click(saveButton);
    await waitFor(() => expect(mocks.updateSchedule).toHaveBeenCalledTimes(2));
    expect(mocks.updateSchedule.mock.calls[1][1].dependencies).toEqual([
      expect.objectContaining({ source_schedule_id: source.id }),
    ]);
  });

  it('keeps the detail and edit values when a save fails, then allows a retry', async () => {
    render(<ScheduleList active />);
    const { detail, editor } = await openDetailEditor();
    mocks.updateSchedule.mockRejectedValueOnce(new Error('save failed'));
    editSchedule(editor);

    await waitFor(() => expect(mocks.updateSchedule).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(within(editor).getByRole('button', { name: 'taskCenter.scheduleSaveBtn' })).not.toHaveClass('ant-btn-loading'));
    expect(within(detail).getByText(schedule.name)).toBeInTheDocument();
    expect(within(detail).getByText(/10:39.*Asia\/Shanghai/)).toBeInTheDocument();
    expect(timeInput(editor)).toHaveValue('11:07');
    expect(mocks.listSchedules).toHaveBeenCalledTimes(1);

    mocks.listSchedules.mockResolvedValue({ items: [updatedSchedule] });
    fireEvent.click(within(editor).getByRole('button', { name: 'taskCenter.scheduleSaveBtn' }));
    await expectUpdatedDetail(detail);
    expect(mocks.updateSchedule).toHaveBeenCalledTimes(2);
  });
});
