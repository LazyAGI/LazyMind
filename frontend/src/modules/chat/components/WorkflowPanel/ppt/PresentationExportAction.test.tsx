import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { axiosInstance, BASE_URL } from '@/components/request';
import type { TabDef, WorkflowSession, WorkflowTabAction } from '@/modules/chat/store/workflowPanel';
import { PresentationExportAction } from './PresentationExportAction';

const action: WorkflowTabAction = {
  id: 'export', label: '导出测试', type: 'export', provider: 'presentation',
  inputs: { pages: 'preview_html' },
};
const tab: TabDef = { id: 'preview', label: '预览', slots: [] };
const session: WorkflowSession = {
  session_id: 'presentation-test', conversation_id: 'conversation-test', workflow_id: 'ppt-workflow',
  workflow_mode: 'auto', status: 'completed', current_step_id: '', created_at: '', updated_at: '',
  slots: [{
    slot_id: 'preview_html', slot: 'preview_html', sort_order: 1, revision: 1,
    selected: true, created_at: '', artifact_value: '<html><head><style>' +
      '@font-face{font-family:Unused;src:url(unused.woff)}' +
      ':root{--deck-font:"Songti SC",serif}p{font-family:var(--deck-font)}' +
      '</style></head><body style="font-family:Georgia"><p>内容</p></body></html>',
  }],
};

beforeEach(() => {
  vi.spyOn(axiosInstance, 'get').mockResolvedValue({ data: { formats: [{ id: 'editable-pptx', available: true }] } });
  vi.spyOn(axiosInstance, 'post').mockResolvedValue({ data: new Blob(['pptx']) });
  vi.stubGlobal('URL', Object.assign(class extends URL {}, {
    createObjectURL: vi.fn(() => 'blob:test'),
    revokeObjectURL: vi.fn(),
  }));
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

it.each([true, false])('exports editable PPTX with font embedding set to %s', async (embedFonts) => {
  render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <PresentationExportAction action={action} tab={tab} session={session} rows={[1]} />
      </MemoryRouter>
    </ConfigProvider>,
  );
  fireEvent.click(screen.getByRole('button', { name: '导出测试' }));
  await waitFor(() => expect(screen.getByText('幻灯片中指定的字体（Songti SC, Georgia）')).toBeVisible());
  expect(screen.queryByText(/Unused/)).toBeNull();
  expect(screen.getByText(/请在该设备下载并安装对应字体/)).toBeVisible();
  const checkbox = screen.getByRole('checkbox', { name: '在可编辑 PPTX 中嵌入字体' });
  expect(checkbox).toBeChecked();
  if (!embedFonts) fireEvent.click(checkbox);

  fireEvent.click(screen.getByRole('button', { name: '可编辑 PPTX' }));
  await waitFor(() => expect(axiosInstance.post).toHaveBeenCalledWith(
    BASE_URL + '/api/core/exporters/presentation:export',
    expect.objectContaining({ format: 'editable-pptx', embed_fonts: embedFonts }),
    expect.objectContaining({ responseType: 'blob' }),
  ));
});
