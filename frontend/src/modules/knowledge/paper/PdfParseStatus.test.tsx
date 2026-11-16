import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import PdfParseStatus from './PdfParseStatus';
describe('PDF parse status', () => {
  it('refreshes unknown without submitting a task', async () => {
    const refresh = vi.fn().mockResolvedValue(undefined), parse = vi.fn();
    render(<PdfParseStatus status="unknown" onRefresh={refresh} onParse={parse} />);
    fireEvent.click(screen.getByRole('button', { name: 'PDF 解析：状态未知' }));
    fireEvent.click(await screen.findByRole('button', { name: '刷新状态' }));
    await waitFor(() => expect(refresh).toHaveBeenCalledOnce());
    expect(screen.queryByRole('button', { name: /开始解析|重试解析/ })).not.toBeInTheDocument();
    expect(parse).not.toHaveBeenCalled();
  });
  it('distinguishes stored and parsed', () => {
    const { rerender } = render(<PdfParseStatus status="not_requested" />);
    expect(screen.getByText('尚未解析')).toBeInTheDocument();
    rerender(<PdfParseStatus status="ready" />);
    expect(screen.getByText('已解析')).toBeInTheDocument();
    expect(screen.queryByText('尚未解析')).not.toBeInTheDocument();
  });
  it('provides explicit retry and collapsed diagnostics', async () => {
    const retry = vi.fn();
    render(<PdfParseStatus status="failed" error="TLS mismatch" onParse={retry} />);
    expect(screen.queryByText('TLS mismatch')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'PDF 解析：解析失败' }));
    expect(await screen.findByText('查看详情')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重试解析' }));
    expect(retry).toHaveBeenCalledOnce();
  });
});
