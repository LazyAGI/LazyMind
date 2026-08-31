import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import WorkflowInstalledView from './WorkflowInstalledView';

const workflowApiMocks = vi.hoisted(() => ({
  listWorkflowDrafts: vi.fn(),
  listBuiltinWorkflows: vi.fn(),
  listUserWorkflowSettings: vi.fn(),
  setUserWorkflowCallMode: vi.fn(),
}));

vi.mock('@/modules/workflow/workflowDraftApi', () => ({
  deleteWorkflowDraft: vi.fn(),
  updateWorkflowDraftContent: vi.fn(),
  ...workflowApiMocks,
}));

const labels: Record<string, string> = {
  'admin.memoryWorkflowColId': '工作流标识',
  'admin.memoryWorkflowColName': '显示名称',
  'admin.memoryWorkflowColType': '类型',
  'admin.memoryWorkflowColStatus': '状态',
  'admin.memoryWorkflowColUpdatedAt': '最后更新',
  'admin.memoryWorkflowColCallMode': '调用方式',
  'admin.memoryWorkflowCallModeAuto': '智能匹配',
  'admin.memoryWorkflowCallModeAutoDesc': '系统判断相关时使用',
  'admin.memoryWorkflowCallModeManual': '仅手动调用',
  'admin.memoryWorkflowCallModeManualDesc': '用户明确调用时使用',
  'admin.memoryWorkflowCallModeDisabled': '暂停使用',
  'admin.memoryWorkflowCallModeDisabledDesc': '不参与对话，但保留工作流与配置',
  'admin.memoryWorkflowCallModeUpdated': '工作流调用方式已更新',
  'admin.memoryWorkflowCallModeUpdateFailed': '更新工作流调用方式失败',
  'admin.memoryWorkflowTypeBuiltin': '内置',
  'admin.memoryWorkflowActionView': '查看工作流',
  'admin.memoryWorkflowSearchPlaceholder': '搜索工作流名称...',
  'admin.memoryWorkflowFilterAll': '全部',
  'admin.memoryWorkflowFilterBuiltin': '内置',
  'admin.memoryWorkflowFilterCustom': '自定义',
  'admin.memoryWorkflowEmptyNoResult': '没有工作流',
};

const t = (key: string) => labels[key] || key;

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
});

function renderView() {
  render(
    <MemoryRouter>
      <WorkflowInstalledView t={t} onNewWorkflow={vi.fn()} />
    </MemoryRouter>,
  );
}

async function openCallModeSelect() {
  const combobox = await screen.findByRole('combobox', { name: '调用方式: AI 图片生成' });
  fireEvent.mouseDown(combobox);
  return combobox;
}

describe('WorkflowInstalledView call mode', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    workflowApiMocks.listWorkflowDrafts.mockResolvedValue({ records: [], total: 0 });
    workflowApiMocks.listBuiltinWorkflows.mockResolvedValue([{
      id: 'image-workflow',
      name: 'AI 图片生成',
      description: '',
      steps: [],
    }]);
    workflowApiMocks.listUserWorkflowSettings.mockResolvedValue([{
      workflow_ref: 'builtin:image-workflow',
      workflow_id: 'image-workflow',
      name: 'AI 图片生成',
      description: '',
      when_to_use: '',
      source_type: 'builtin',
      revision_id: '',
      revision_no: 0,
      remote_root: '',
      enabled: true,
      call_mode: 'manual',
      status: 'published',
    }]);
  });

  it('shows the three call methods and prevents another change while saving', async () => {
    let resolveUpdate!: () => void;
    workflowApiMocks.setUserWorkflowCallMode.mockImplementation(() => new Promise<void>((resolve) => {
      resolveUpdate = resolve;
    }));
    renderView();

    const combobox = await openCallModeSelect();
    expect(combobox.closest('.ant-select')).toHaveTextContent('仅手动调用');
    expect(await screen.findByText('系统判断相关时使用')).toBeInTheDocument();
    expect(screen.getByText('用户明确调用时使用')).toBeInTheDocument();
    expect(screen.getByText('不参与对话，但保留工作流与配置')).toBeInTheDocument();

    fireEvent.click(screen.getByText('智能匹配'));

    expect(workflowApiMocks.setUserWorkflowCallMode).toHaveBeenCalledWith('builtin:image-workflow', 'auto');
    expect(combobox).toBeDisabled();
    resolveUpdate();
    await waitFor(() => expect(combobox).not.toBeDisabled());
    expect(combobox.closest('.ant-select')).toHaveTextContent('智能匹配');
  });

  it('rolls back the selected call method when saving fails', async () => {
    workflowApiMocks.setUserWorkflowCallMode.mockRejectedValue(new Error('request failed'));
    renderView();

    const combobox = await openCallModeSelect();
    fireEvent.click(screen.getByText('暂停使用'));

    await waitFor(() => {
      expect(workflowApiMocks.setUserWorkflowCallMode).toHaveBeenCalledWith('builtin:image-workflow', 'disabled');
      expect(combobox.closest('.ant-select')).toHaveTextContent('仅手动调用');
    });
  });
});
