import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { TFunction } from 'i18next';
import type { FeishuAuthAccount } from '@/modules/dataSource/common/feishuAccounts';
import FeishuAccountTable from './FeishuAccountTable';

const account = {
  id: 'fixture', name: 'Fixture', appId: 'cli_fixture', appSecret: '',
  status: 'expired', chatEnabled: true, canUseChat: false,
  connection: { connectionId: 'fixture' }, createdAt: '2026-09-21T00:00:00Z',
} as FeishuAuthAccount;
const props = {
  t: ((key: string) => key) as TFunction,
  accountsLoading: false, onAuthorize: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(),
};

describe('Feishu chat preference switch', () => {
  it('shows an enabled preference even when expired and disabled for editing', () => {
    const toggle = vi.fn();
    render(<FeishuAccountTable {...props} accounts={[account]} onToggleChat={toggle} />);
    const control = screen.getByRole('switch');
    expect(control).toHaveAttribute('aria-checked', 'true');
    expect(control).toBeDisabled();
    fireEvent.click(control);
    expect(toggle).not.toHaveBeenCalled();
  });

  it('blocks repeated updates while saving', () => {
    const toggle = vi.fn();
    render(<FeishuAccountTable {...props} accounts={[{ ...account, status: 'connected' }]}
      chatUpdatingAccountIds={['fixture']} onToggleChat={toggle} />);
    expect(screen.getByRole('switch')).toHaveAttribute('aria-busy', 'true');
    fireEvent.click(screen.getByRole('switch'));
    expect(toggle).not.toHaveBeenCalled();
  });

  it('shows the migration requirement separately from the stored preference', () => {
    render(<FeishuAccountTable {...props} accounts={[{ ...account, status: 'connected', connection_method: 'managed_oauth' }]}
      onToggleChat={vi.fn()} />);
    expect(screen.getByText('modelProvider.cloudDocuments.feishuReconnectRequired')).toBeInTheDocument();
    expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('switch')).toBeDisabled();
  });
});
