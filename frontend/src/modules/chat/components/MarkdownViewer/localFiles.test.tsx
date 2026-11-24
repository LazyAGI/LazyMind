import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { notification } from 'antd';
import MarkdownViewer from './index';
import { localFilePath } from './remarkLocalFiles';

afterEach(async () => { cleanup(); await act(async () => { notification.destroy(); }); delete (window as any).lazymindDesktop; });

it('opens Codex output references and reveals their original file on right click', async () => {
  const localFileAction = vi.fn().mockResolvedValue({ ok: true });
  (window as any).lazymindDesktop = { platform: 'darwin', localFileAction };
  render(<MarkdownViewer>{':codex-file-citation{path="/Users/example/中文 output/剧本.docx" purpose="output"}'}</MarkdownViewer>);
  const link = screen.getByRole('link', { name: '剧本.docx' });
  fireEvent.click(link);
  expect(localFileAction).toHaveBeenCalledWith({ path: '/Users/example/中文 output/剧本.docx', action: 'open' });
  fireEvent.contextMenu(link);
  fireEvent.click(await screen.findByText('在 Finder 中显示'));
  await waitFor(() => expect(localFileAction).toHaveBeenCalledWith({ path: '/Users/example/中文 output/剧本.docx', action: 'reveal' }));
});

it('opens source links without line suffixes and uses the OS browser for web links', () => {
  const localFileAction = vi.fn().mockResolvedValue({ ok: true });
  const openChatExternal = vi.fn().mockResolvedValue(undefined);
  (window as any).lazymindDesktop = { localFileAction, openChatExternal };
  render(<MarkdownViewer>{'[source](/Users/example/main.go:12:3) [web](https://example.com/page)'}</MarkdownViewer>);
  fireEvent.click(screen.getByRole('link', { name: 'source' }));
  expect(localFileAction).toHaveBeenCalledWith({ path: '/Users/example/main.go', action: 'open' });
  fireEvent.click(screen.getByRole('link', { name: 'web' }));
  expect(openChatExternal).toHaveBeenCalledWith('https://example.com/page');
});

it('preserves code examples and application routes', () => {
  render(<MarkdownViewer>{'`:codex-file-citation{path="/tmp/example.docx"}`\n\n[route](/agent/chat/home)'}</MarkdownViewer>);
  expect(screen.queryByRole('link', { name: 'example.docx' })).toBeNull();
  expect(screen.getByRole('link', { name: 'route' })).toHaveAttribute('href', '/agent/chat/home');
});

it('prefers the shared browser action for web links while retaining native mail links', async () => {
  const openExternal = vi.fn().mockResolvedValue(undefined);
  const openChatExternal = vi.fn().mockResolvedValue(undefined);
  (window as any).lazymindDesktop = { openExternal, openChatExternal };
  render(<MarkdownViewer>{'[web](https://example.com/page) [mail](mailto:test@example.com)'}</MarkdownViewer>);
  const web = screen.getByRole('link', { name: 'web' });
  expect(web).toHaveAttribute('data-external-link', 'true');
  fireEvent.click(web);
  await waitFor(() => expect(openExternal).toHaveBeenCalledWith('https://example.com/page'));
  expect(openChatExternal).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('link', { name: 'mail' }));
  expect(openChatExternal).toHaveBeenCalledWith('mailto:test@example.com');
});

it('keeps retry available when a Markdown web link fails to open on desktop', async () => {
  const openExternal = vi.fn().mockRejectedValueOnce(new Error('unavailable')).mockResolvedValue(undefined);
  const openChatExternal = vi.fn().mockResolvedValue(undefined);
  (window as any).lazymindDesktop = { openExternal, openChatExternal };
  render(<MarkdownViewer>{'[web](https://example.com/retry)'}</MarkdownViewer>);
  fireEvent.click(screen.getByRole('link', { name: 'web' }));
  fireEvent.click(await screen.findByRole('button', { name: /重\s*试/ }));
  await waitFor(() => expect(openExternal).toHaveBeenCalledTimes(2));
  expect(openExternal).toHaveBeenLastCalledWith('https://example.com/retry');
  expect(openChatExternal).not.toHaveBeenCalled();
});

it('handles Windows and encoded file URLs while rejecting unsafe targets', () => {
  expect(localFilePath('C:\\Users\\me\\report.docx:20')).toBe('C:\\Users\\me\\report.docx');
  expect(localFilePath('file:///C:/Users/me/a%20b.docx')).toBe('C:/Users/me/a b.docx');
  expect(localFilePath('/home/me/main.py#L12')).toBe('/home/me/main.py');
  for (const value of ['javascript:alert(1)', '/api/core/test', 'https://example.com/Users/me/a', 'file://server/share/a', '/tmp/a%00b']) expect(localFilePath(value)).toBe('');
});
