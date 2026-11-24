import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import ExternalLink from './index';
import { externalLinkProps, openExternalUrl } from '@/runtime/externalNavigation';

const { error, success, destroy } = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn(), destroy: vi.fn() }));
vi.mock('antd', () => ({ Button: 'button', notification: { error, success, destroy } }));
vi.mock('i18next', () => ({ default: { language: 'zh-CN' } }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.clearAllMocks(); delete window.lazymindDesktop; });

it('labels external links and hands one click to the desktop once', async () => {
  let finish!: () => void;
  const openExternal = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
  (window as any).lazymindDesktop = { openExternal };
  render(<ExternalLink href='https://example.org/docs'>官方文档</ExternalLink>);
  const link = screen.getByRole('link', { name: '官方文档' });
  expect(link).toHaveAttribute('data-external-link', 'true');
  expect(link).toHaveAttribute('title', '在默认浏览器中打开');
  fireEvent.click(link); fireEvent.click(link);
  expect(openExternal).toHaveBeenCalledTimes(1);
  expect(openExternal).toHaveBeenCalledWith('https://example.org/docs');
  finish(); await waitFor(() => expect(error).not.toHaveBeenCalled());
});

it('keeps internal routes, downloads and application protocols separate', () => {
  render(<><ExternalLink href='/lib/knowledge/list'>知识库</ExternalLink><ExternalLink href='https://example.org/file' download>下载</ExternalLink><ExternalLink href='obsidian://open?path=%2Ftmp%2Fa.md'>笔记</ExternalLink></>);
  for (const name of ['知识库', '下载', '笔记']) expect(screen.getByRole('link', { name })).not.toHaveAttribute('data-external-link');
  expect(screen.getByRole('link', { name: '知识库' })).not.toHaveAttribute('target');
});

it('blocks unsafe URLs even when used on an existing button', () => {
  for (const href of ['javascript:alert(1)', 'data:text/html,bad', 'https://user:pass@example.org', 'file:///tmp/a']) {
    expect(externalLinkProps(href).href).toBeUndefined();
  }
});

it('uses native new-tab links on the web and respects a cancelled click', () => {
  const open = vi.spyOn(window, 'open').mockReturnValue(null);
  const props = externalLinkProps('https://example.org');
  expect(props.target).toBe('_blank'); expect(props.rel).toBe('noopener noreferrer');
  render(<ExternalLink href='https://example.org' onClick={event => event.preventDefault()}>来源</ExternalLink>);
  fireEvent.click(screen.getByRole('link'));
  expect(open).not.toHaveBeenCalled();
  open.mockRestore();
});

it('reports a failed handoff without leaking the exception and supports a later retry', async () => {
  const openExternal = vi.fn().mockRejectedValueOnce(new Error('secret token')).mockResolvedValue(undefined);
  (window as any).lazymindDesktop = { openExternal };
  await openExternalUrl('https://example.org');
  expect(error).toHaveBeenCalledOnce();
  expect(error.mock.calls[0][0].message).toBe('无法打开浏览器，请重试');
  expect(JSON.stringify(error.mock.calls)).not.toContain('secret token');
  await openExternalUrl('https://example.org'); expect(openExternal).toHaveBeenCalledTimes(2);
});

it('opens a desktop external link once for a middle click and ignores a right click', async () => {
  const openExternal = vi.fn().mockResolvedValue(undefined);
  window.lazymindDesktop = { openExternal };
  render(<ExternalLink href='https://example.org/docs'>来源</ExternalLink>);
  const link = screen.getByRole('link');
  fireEvent(link, new MouseEvent('auxclick', { button: 2, bubbles: true, cancelable: true }));
  expect(openExternal).not.toHaveBeenCalled();
  fireEvent(link, new MouseEvent('auxclick', { button: 1, bubbles: true, cancelable: true }));
  await waitFor(() => expect(openExternal).toHaveBeenCalledOnce());
  expect(openExternal).toHaveBeenCalledWith('https://example.org/docs');
});

it('honors the existing click handler when it cancels navigation on desktop', () => {
  const openExternal = vi.fn();
  window.lazymindDesktop = { openExternal };
  render(<ExternalLink href='https://example.org' onClick={event => event.preventDefault()}>来源</ExternalLink>);
  fireEvent.click(screen.getByRole('link'));
  expect(openExternal).not.toHaveBeenCalled();
});

it('reports a blocked web popup as a recoverable failure', async () => {
  vi.spyOn(window, 'open').mockReturnValue(null);
  await openExternalUrl('https://example.org/docs');
  expect(error).toHaveBeenCalledOnce();
  render(error.mock.calls[0][0].actions);
  expect(screen.getByRole('button', { name: '重试' })).toBeEnabled();
  expect(screen.getByRole('button', { name: '复制链接' })).toBeEnabled();
});

it('the displayed retry action opens the original URL after the browser recovers', async () => {
  const openExternal = vi.fn().mockRejectedValueOnce(new Error('unavailable')).mockResolvedValue(undefined);
  window.lazymindDesktop = { openExternal };
  await openExternalUrl('https://example.org/docs?item=fixture#section');
  render(error.mock.calls[0][0].actions);
  fireEvent.click(screen.getByRole('button', { name: '重试' }));
  await waitFor(() => expect(openExternal).toHaveBeenCalledTimes(2));
  expect(openExternal.mock.calls[1][0]).toBe('https://example.org/docs?item=fixture#section');
  expect(destroy).toHaveBeenCalledWith('external-browser');
});

it.each([true, false])('copying a failed link reports the clipboard outcome safely (success: %s)', async copies => {
  const writeText = copies ? vi.fn().mockResolvedValue(undefined) : vi.fn().mockRejectedValue(new Error('private clipboard failure'));
  vi.stubGlobal('navigator', { clipboard: { writeText } });
  window.lazymindDesktop = { openExternal: vi.fn().mockRejectedValue(new Error('unavailable')) };
  await openExternalUrl('https://example.org/docs');
  render(error.mock.calls[0][0].actions);
  fireEvent.click(screen.getByRole('button', { name: '复制链接' }));
  await waitFor(() => expect(copies ? success : error).toHaveBeenCalledWith(expect.objectContaining({
    key: 'external-browser-copy', message: copies ? '链接已复制' : '无法复制链接，请重试',
  })));
  expect(writeText).toHaveBeenCalledWith('https://example.org/docs');
  expect(JSON.stringify(error.mock.calls)).not.toContain('private clipboard failure');
});
