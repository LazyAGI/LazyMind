import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import Boundary, { GlobalErrorFallback } from './index';

vi.mock('../../i18n', () => ({ default: { language: 'zh-CN' } }));
vi.mock('../../globalState', () => ({ BASENAME: '/workspace' }));
afterEach(() => { cleanup(); vi.restoreAllMocks(); delete window.lazymindDesktop; });

it('replaces a rendering failure with a safe retry page', () => {
  vi.spyOn(console, 'error').mockImplementation(() => {});
  const notifyAppReady = vi.fn();
  window.lazymindDesktop = { notifyAppReady };
  const Broken = () => { throw new Error('private exception'); };
  render(<Boundary><Broken /></Boundary>);
  expect(screen.getByRole('button', { name: '重试' })).toBeVisible();
  expect(screen.getByRole('button', { name: '重试' })).toHaveFocus();
  expect(screen.getByRole('button', { name: '返回首页' })).toBeVisible();
  expect(screen.queryByText('private exception')).toBeNull();
  expect(notifyAppReady).toHaveBeenCalledOnce();
});

it('runs recovery once and makes both actions unavailable during navigation', () => {
  const retry = vi.fn(), home = vi.fn();
  render(<GlobalErrorFallback onRetry={retry} onHome={home} />);
  const button = screen.getByRole('button', { name: '重试' });
  fireEvent.click(button); fireEvent.click(button); fireEvent.click(screen.getByRole('button', { name: '返回首页' }));
  expect(retry).toHaveBeenCalledOnce(); expect(home).not.toHaveBeenCalled();
  expect(button).toBeDisabled();
});
