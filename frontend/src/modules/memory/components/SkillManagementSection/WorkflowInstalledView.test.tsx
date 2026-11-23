import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import WorkflowInstalledView from './WorkflowInstalledView';

const workflowApiMocks = vi.hoisted(() => ({
  listWorkflowDrafts: vi.fn(),
  listBuiltinWorkflows: vi.fn(),
  listUserWorkflowSettings: vi.fn(),
  setUserWorkflowCallMode: vi.fn(),
  copyBuiltinWorkflow: vi.fn(),
  copyWorkflowDraft: vi.fn(),
}));

vi.mock('@/runtime/mode', async (load) => ({ ...await load<object>(), isDesktopRuntime: () => false }));

vi.mock('@/modules/workflow/workflowDraftApi', () => ({
  deleteWorkflowDraft: vi.fn(),
  updateWorkflowDraftContent: vi.fn(),
  ...workflowApiMocks,
}));

const labels: Record<string, string> = {
  'admin.memoryWorkflowColId': '工作流标识',
  'admin.memoryWorkflowColName': '显示名称',
  'admin.memorySkillOrigin': '来源',
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
  'admin.memoryWorkflowActionUse': '使用该工作流',
  'admin.memoryWorkflowActionView': '查看工作流',
  'admin.memoryWorkflowActionCopy': '复制工作流',
  'admin.memoryWorkflowCopyName': '{{name}} 副本',
  'admin.memoryWorkflowCopySuccess': '工作流已复制',
  'admin.memoryWorkflowCopyFailed': '复制工作流失败',
  'admin.memoryWorkflowSearchPlaceholder': '搜索工作流名称...',
  'admin.memoryWorkflowFilterAll': '全部',
  'admin.memoryWorkflowFilterBuiltin': '内置',
  'admin.memoryWorkflowFilterCustom': '技能转换',
  'admin.memoryWorkflowFilterAi': 'AI生成',
  'admin.memoryWorkflowFilterBlank': '自定义',
  'admin.memoryWorkflowEmptyNoResult': '没有工作流',
};

const t = (key: string, options?: Record<string, unknown>) => {
  let value = labels[key] || key;
  Object.entries(options ?? {}).forEach(([name, replacement]) => {
    value = value.replace(`{{${name}}}`, String(replacement));
  });
  return value;
};

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

function Location() { const location = useLocation(); return <output data-testid="location">{JSON.stringify({ pathname: location.pathname, state: location.state })}</output>; }

function renderView() {
  render(
    <MemoryRouter>
      <WorkflowInstalledView t={t} onNewWorkflow={vi.fn()} />
      <Location />
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

  it('keeps only type filters and the deployment workflow list', async () => {
    renderView();
    expect(await screen.findByText('AI 图片生成', { exact: true })).toBeVisible();
    expect(screen.getAllByRole('tab')).toHaveLength(5);
    expect(screen.getByRole('tab', { name: 'AI生成' })).toBeVisible();
    expect(screen.getByRole('tab', { name: '自定义' })).toBeVisible();
    expect(screen.getByRole('button', { name: '搜索工作流名称...' })).toBeVisible();
    fireEvent.click(screen.getByRole('tab', { name: '技能转换' }));
    expect(screen.queryByText('AI 图片生成', { exact: true })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '内置' }));
    expect(screen.getByText('AI 图片生成', { exact: true })).toBeVisible();
  });

  it('filters drafts by skill conversion, AI generation, and custom sources', async () => {
    workflowApiMocks.listWorkflowDrafts.mockResolvedValue({
      records: [
        { id: 'skill-draft', name: 'from-skill', source_type: 'skill', generate_status: 'done', updated_at: '2026-09-07T00:00:00Z' },
        { id: 'ai-draft', name: 'from-ai', source_type: 'ai', generate_status: 'done', updated_at: '2026-09-07T00:00:00Z' },
        { id: 'blank-draft', name: 'from-blank', source_type: 'blank', generate_status: 'done', updated_at: '2026-09-07T00:00:00Z' },
      ],
      total: 3,
    });
    renderView();
    expect(await screen.findByText('from-skill', { exact: true })).toBeVisible();
    fireEvent.click(screen.getByRole('tab', { name: '技能转换' }));
    expect(screen.getByText('from-skill', { exact: true })).toBeVisible();
    expect(screen.queryByText('from-ai', { exact: true })).not.toBeInTheDocument();
    expect(screen.queryByText('from-blank', { exact: true })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: 'AI生成' }));
    expect(screen.getByText('from-ai', { exact: true })).toBeVisible();
    expect(screen.queryByText('from-skill', { exact: true })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '自定义' }));
    expect(screen.getByText('from-blank', { exact: true })).toBeVisible();
    expect(screen.queryByText('from-ai', { exact: true })).not.toBeInTheDocument();
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

  it('copies a built-in workflow into an editable draft', async () => {
    workflowApiMocks.copyBuiltinWorkflow.mockResolvedValue({ id: 'new-draft-id' });
    renderView();

    fireEvent.click(await screen.findByRole('button', { name: '复制工作流' }));

    await waitFor(() => {
      expect(workflowApiMocks.copyBuiltinWorkflow).toHaveBeenCalledWith('image-workflow', 'AI 图片生成 副本');
    });
  });
  it('shows use/view/copy in order and launches the exact workflow without running or copying it', async () => {
    renderView();
    const use = await screen.findByRole('button', { name: '使用该工作流' });
    const actions = use.closest('.memory-resource-actions')!;
    expect(within(actions as HTMLElement).getAllByRole('button').map(button => button.getAttribute('aria-label')))
      .toEqual(['使用该工作流', '查看工作流', '复制工作流']);
    expect(use.querySelector('[data-workflow-icon="circle-arrow-right"]')).not.toBeNull();
    fireEvent.click(use);
    expect(JSON.parse(screen.getByTestId('location').textContent!)).toEqual({ pathname: '/agent/chat/home', state: { workflowLaunch: { workflowRef: 'builtin:image-workflow', name: 'AI 图片生成' } } });
    expect(workflowApiMocks.copyBuiltinWorkflow).not.toHaveBeenCalled();
    expect(workflowApiMocks.setUserWorkflowCallMode).not.toHaveBeenCalled();
  });

  it('stably prioritizes the full result before pagination and keeps the order after filtering', async () => {
    const otherIds = Array.from({ length: 22 }, (_, i) => `other-${i}`);
    const ids = [...otherIds, 'image-workflow', 'writer-workflow', 'paper-writing', 'image-workflow-v2', 'paper_ppt_with_reflection_mock', 'ppt-workflow', 'academic_research_pipeline'];
    workflowApiMocks.listBuiltinWorkflows.mockResolvedValue(ids.map(id => ({ id, name: `name:${id}`, steps: [] })));
    renderView();
    await screen.findByText('name:ppt-workflow');
    const names = () => [...document.querySelectorAll('.memory-term-link')].map(node => node.textContent);
    const priority = ['ppt-workflow', 'image-workflow-v2', 'paper-writing', 'writer-workflow', 'academic_research_pipeline', 'paper_ppt_with_reflection_mock'];
    expect(names()).toEqual([...priority, ...otherIds.slice(0, 14)].map(id => `name:${id}`));
    fireEvent.click(screen.getByTitle('2'));
    expect(names()).toEqual([...otherIds.slice(14), 'image-workflow'].map(id => `name:${id}`));
    fireEvent.click(screen.getByRole('tab', { name: '内置' }));
    expect(names().slice(0, priority.length)).toEqual(priority.map(id => `name:${id}`));
  });

  it('loads all server pages before displaying the catalog in non-desktop mode', async () => {
    workflowApiMocks.listWorkflowDrafts.mockResolvedValueOnce({ records: [], total: 101 })
      .mockResolvedValueOnce({ records: [{ id: 'last', name: 'Last draft', updated_at: '2026-09-07', source_type: 'ai' }], total: 101 });
    renderView();
    expect(await screen.findByText('Last draft')).toBeVisible();
    expect(workflowApiMocks.listWorkflowDrafts).toHaveBeenNthCalledWith(2, { page: 2, pageSize: 100 });
  });

  it('prevents using paused workflows and unpublished drafts', async () => {
    workflowApiMocks.listUserWorkflowSettings.mockResolvedValue([{ workflow_ref: 'builtin:image-workflow', call_mode: 'disabled' }]);
    workflowApiMocks.listWorkflowDrafts.mockResolvedValue({ records: [{ id: 'unpublished', name: 'Draft', source_type: 'ai', updated_at: '2026-09-07' }], total: 1 });
    renderView();
    await screen.findByText('Draft');
    expect(screen.getAllByRole('button', { name: '使用该工作流' })).toHaveLength(2);
    screen.getAllByRole('button', { name: '使用该工作流' }).forEach(button => expect(button).toBeDisabled());
  });

  it('adapts pagination to the body height and retains the current item across resize', async () => {
    let height = 300;
    const clientHeight = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('ant-table-body') ? height : 0;
    });
    const rowBounds = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const footer = this.querySelector<HTMLElement>('.ant-table-pagination');
      const footerStyle = footer ? getComputedStyle(footer) : undefined;
      const footerMargins = (parseFloat(footerStyle?.marginTop || '') || 0) + (parseFloat(footerStyle?.marginBottom || '') || 0);
      const elementHeight = this.classList.contains('ant-table-row') ? 60
        : this.classList.contains('memory-list-content') ? height + footerMargins
        : this.classList.contains('ant-table-body') ? height : 0;
      return { height: elementHeight, width: 0, top: 0, bottom: elementHeight, left: 0, right: 0, x: 0, y: 0, toJSON: () => ({}) };
    });
    try {
      workflowApiMocks.listBuiltinWorkflows.mockResolvedValue(Array.from({ length: 30 }, (_, i) => ({ id: `other-${i}`, name: `Row ${i}`, steps: [] })));
      renderView();
      const names = () => [...document.querySelectorAll('.memory-term-link')].map(node => node.textContent);
      await waitFor(() => expect(names()).toHaveLength(5));
      fireEvent.click(screen.getByTitle('2'));
      expect(names()[0]).toBe('Row 5');
      act(() => { height = 600; window.dispatchEvent(new Event('resize')); });
      await waitFor(() => expect(names()).toHaveLength(10));
      expect(names()).toContain('Row 5');
      fireEvent.click(screen.getByTitle('2'));
      expect(names()[0]).toBe('Row 10');
      act(() => { height = 120; window.dispatchEvent(new Event('resize')); });
      await waitFor(() => expect(names()).toEqual(['Row 10', 'Row 11']));
      fireEvent.click(screen.getByRole('tab', { name: '内置' }));
      expect(names()).toEqual(['Row 0', 'Row 1']);
    } finally {
      clientHeight.mockRestore();
      rowBounds.mockRestore();
    }
  });

});
