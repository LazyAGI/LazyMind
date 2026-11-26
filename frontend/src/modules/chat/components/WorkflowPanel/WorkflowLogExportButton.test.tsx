import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import JSZip from '@progress/jszip-esm';
import { WorkflowLogExportButton } from './WorkflowLogExportButton';

const mocks = vi.hoisted(() => ({
  read: vi.fn(), host: vi.fn(), download: vi.fn(), warning: vi.fn(), error: vi.fn(),
}));

vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => ({ getRunLogs: mocks.read }) }));
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getSessionIdentity: () => 'owner' } }));
vi.mock('@/runtime/assistantSession', () => ({ assistantBridgeJSON: mocks.host, AssistantBridgeRequestError: Error }));
vi.mock('@/modules/chat/utils/workflowDownload', async importOriginal => ({
  ...await importOriginal<typeof import('@/modules/chat/utils/workflowDownload')>(),
  downloadWorkflowFile: mocks.download,
  readEmbeddedWorkflowHostLogs: () => undefined,
}));
vi.mock('antd', () => ({
  Button: ({ onClick, children, loading }: { onClick: () => void; children: string; loading: boolean }) => (
    <button onClick={onClick} aria-busy={loading}>{children}</button>
  ),
  message: { warning: mocks.warning, error: mocks.error },
}));

beforeEach(() => {
  vi.resetAllMocks();
});

it.each([true, false])('settles the native save and only warns for a failure (cancelled: %s)', async cancelled => {
  const parent = window.parent;
  const location = window.location.href;
  const host = 'http://127.0.0.1:54321';
  const bridgeId = crypto.randomUUID();
  const parentFrame = { postMessage: vi.fn((request: { requestId: string; action: string }) => {
    const result = request.action === 'capabilities' ? { ok: true } : { ok: false, cancelled };
    window.dispatchEvent(new MessageEvent('message', { origin: host, source: parentFrame as unknown as Window, data: {
      type: 'lazymind.workflow.download.result', bridgeId, requestId: request.requestId, ...result,
    } }));
  }) };
  Object.defineProperty(window, 'parent', { configurable: true, value: parentFrame });
  history.replaceState({}, '', `/workflow-runs/run/embed?downloadHostOrigin=${encodeURIComponent(host)}&downloadBridgeId=${bridgeId}`);
  try {
    const { downloadWorkflowFile } = await vi.importActual<typeof import('@/modules/chat/utils/workflowDownload')>('@/modules/chat/utils/workflowDownload');
    mocks.download.mockImplementationOnce(downloadWorkflowFile);
    const time = '2026-11-26T01:00:00Z';
    mocks.read.mockResolvedValue({ data: { result: {
      run_id: 'run', cutoff: time, host_required: false,
      scheduling: [{ time, event: 'workflow.started' }], agent: [],
    } } });
    render(<WorkflowLogExportButton runId="run" />);
    const button = screen.getByRole('button');
    fireEvent.click(button);
    await waitFor(() => expect(parentFrame.postMessage).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(button.getAttribute('aria-busy')).toBe('false'));
    expect(mocks.error).toHaveBeenCalledTimes(cancelled ? 0 : 1);
    expect(mocks.warning).not.toHaveBeenCalled();
  } finally {
    Object.defineProperty(window, 'parent', { configurable: true, value: parent });
    history.replaceState({}, '', location);
  }
});

it.each([true, false])('builds a run-scoped ZIP and keeps missing-source information in the panel (host available: %s)', async available => {
  const time = '2026-11-26T01:00:00Z';
  mocks.read.mockResolvedValue({ data: { result: {
    run_id: 'run', cutoff: time, host_required: true, host_grant: 'private-grant',
    scheduling: [{ time, event: 'workflow.started' }], agent: [],
  } } });
  if (available) {
    mocks.host.mockResolvedValue({ run_id: 'run', entries: [{ time, event: 'tool.result', tool: 'search' }] });
  } else {
    mocks.host.mockRejectedValue(new Error('unavailable'));
  }
  render(<WorkflowLogExportButton runId="run" />);
  fireEvent.click(screen.getByRole('button'));
  await waitFor(() => expect(mocks.download).toHaveBeenCalledTimes(1));
  const { blob, filename } = mocks.download.mock.calls[0][0];
  expect(filename).toBe('workflow-logs-run.zip');
  const archive = await new JSZip().loadAsync(blob);
  expect(Object.keys(archive.files).sort()).toEqual(
    available ? ['external-agent.jsonl', 'lazymind.jsonl', 'summary.txt'] : ['lazymind.jsonl', 'summary.txt'],
  );
  expect(await archive.file('summary.txt')?.async('string')).toBe(`Workflow run ID: run\nExport cutoff (UTC): ${time}\n`);
  expect(await archive.file('lazymind.jsonl')?.async('string')).toContain('workflow.started');
  if (available) {
    expect(await archive.file('external-agent.jsonl')?.async('string')).toContain('"tool":"search"');
  }
  expect(mocks.warning).toHaveBeenCalledTimes(available ? 0 : 1);
  expect(mocks.error).not.toHaveBeenCalled();
});
