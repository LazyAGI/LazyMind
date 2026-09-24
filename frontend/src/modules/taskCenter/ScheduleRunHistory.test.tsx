import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ScheduleRunHistory from './ScheduleRunHistory';
import { listScheduleTasks, type Task, type TaskListResponse } from './api';

vi.mock('./api', () => ({ listScheduleTasks: vi.fn() }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
const run = { id: 'run-1', created_at: '2026-09-24T01:00:00Z', status: 'succeeded' } as Task;
const result = { items: [run], total: 6, page: 1, page_size: 5 };
beforeEach(() => { vi.mocked(listScheduleTasks).mockReset().mockResolvedValue(result); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('schedule execution history', () => {
  it('marks a slow page as loading without presenting previous rows as the last page', async () => {
    let resolvePage!: (value: TaskListResponse) => void;
    render(<ScheduleRunHistory scheduleId='plan' currentId={run.id} onSelect={vi.fn()} />);
    await screen.findByRole('table');
    const content = screen.getByRole('region', { name: 'taskCenter.executionHistory' });
    vi.mocked(listScheduleTasks).mockReturnValueOnce(new Promise(resolve => { resolvePage = resolve; }));
    fireEvent.click(screen.getByTitle('2'));
    expect(content).toHaveAttribute('aria-busy', 'true');
    expect(screen.queryByRole('button', { name: /taskCenter.currentExecution/ })).not.toBeInTheDocument();
    await act(async () => { resolvePage({ ...result, items: [{ ...run, id: 'last' }], page: 2 }); });
    expect(await screen.findByRole('button', { name: /taskCenter.viewExecution/ })).toHaveTextContent('1');
    expect(content).toHaveAttribute('aria-busy', 'false');
  });

  it('paginates and reports a failed page without presenting the previous page as its content', async () => {
    render(<ScheduleRunHistory scheduleId='plan' currentId={run.id} onSelect={vi.fn()} />);
    const selected = await screen.findByRole('button', { name: /taskCenter.currentExecution/ });
    expect(selected).toHaveTextContent('6');
    expect(selected).toHaveAttribute('aria-current', 'true');
    vi.mocked(listScheduleTasks).mockRejectedValueOnce(new Error('offline'));
    fireEvent.click(screen.getByTitle('2'));
    await screen.findByRole('alert');
    expect(screen.queryByRole('button', { name: /taskCenter.currentExecution/ })).not.toBeInTheDocument();
    const second = { ...run, id: 'run-2', status: 'failed' };
    vi.mocked(listScheduleTasks).mockResolvedValue({ ...result, items: [second], page: 2 });
    fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    expect(await screen.findByRole('button', { name: /taskCenter.viewExecution/ })).toHaveTextContent('1');
    expect(listScheduleTasks).toHaveBeenLastCalledWith('plan', 2, 5);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('ignores an old plan response after switching plans', async () => {
    let resolveOld!: (value: TaskListResponse) => void;
    vi.mocked(listScheduleTasks).mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve; }));
    const { rerender } = render(<ScheduleRunHistory key='old' scheduleId='old' currentId='old-run' onSelect={vi.fn()} />);
    rerender(<ScheduleRunHistory key='new' scheduleId='new' currentId='run-1' onSelect={vi.fn()} />);
    await screen.findByRole('button', { name: /taskCenter.currentExecution/ });
    await act(async () => { resolveOld({ ...result, items: [{ ...run, id: 'old-run', status: 'failed' }] }); });
    expect(screen.queryByText('taskCenter.statusFailed')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /taskCenter.currentExecution/ })).toBeInTheDocument();
  });

  it('shows an empty state after loading finishes', async () => {
    vi.mocked(listScheduleTasks).mockResolvedValue({ ...result, items: [], total: 0 });
    render(<ScheduleRunHistory scheduleId='empty' currentId='' onSelect={vi.fn()} />);
    await waitFor(() => expect(screen.getByText('taskCenter.noExecutions')).toBeInTheDocument());
  });
});
