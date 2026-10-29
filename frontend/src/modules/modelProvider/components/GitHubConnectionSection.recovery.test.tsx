import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, expect, it, vi } from 'vitest';
import type { CloudDocumentProvidersVm } from '../hooks/useCloudDocumentProviders';
import GitHubConnectionSection from './GitHubConnectionSection';

afterEach(cleanup);
const words: Record<string, string> = {
  authorizationTitle: '连接 GitHub', quickMode: '快捷授权', manual: '手动 OAuth（高级）',
  cancel: '取消授权', retry: '重试', start: '开始连接', addAccount: '添加账号', serviceUnavailable: '授权服务暂时不可用',
};

function renderUncertain(open: boolean, uncertain = true) {
  const cancel = vi.fn();
  const vm = {
    t: (key: string) => words[key.split('.').pop()!] || key,
    githubDialogOpen: open, githubDialogMode: 'quick', setGitHubDialogOpen: vi.fn(),
    setFeishuSetupModalOpen: vi.fn(), setFeishuSetupIntent: vi.fn(),
    handleOpenGitHubSetup: vi.fn(),
    github: { accounts: [], updating: [], active: false, retryCreate: uncertain,
      notice: 'serviceUnavailable', session: null, cancel, retry: vi.fn(), start: vi.fn() },
  } as unknown as CloudDocumentProvidersVm;
  render(<ConfigProvider theme={{ token: { motion: false } }}>
    <GitHubConnectionSection management vm={vm} logo={<span />} />
  </ConfigProvider>);
  return { cancel, vm };
}

it('the add-account dialog starts a new web authorization explicitly', async () => {
  const { vm } = renderUncertain(true, false);
  const dialog = await screen.findByRole('dialog', { name: '连接 GitHub' });
  await waitFor(() => expect(dialog).toBeVisible());
  fireEvent.click(within(dialog).getByRole('button', { name: '开始连接' }));
  expect(vm.github.start).toHaveBeenCalledWith('', true);
});

it('offers cancellation from the page when a create response is unknown', async () => {
  const { cancel } = renderUncertain(false);
  const button = await screen.findByRole('button', { name: /取消/ });
  expect(button).toBeEnabled();
  fireEvent.click(button);
  expect(cancel).toHaveBeenCalledTimes(1);
});

it('the uncertain-request dialog cancel button cancels the server attempt instead of only hiding the modal', async () => {
  const { cancel } = renderUncertain(true);
  const dialog = await screen.findByRole('dialog', { name: '连接 GitHub' });
  await waitFor(() => expect(dialog).toBeVisible());
  fireEvent.click(within(dialog).getByRole('button', { name: /取消/ }));
  expect(cancel).toHaveBeenCalledTimes(1);
});
