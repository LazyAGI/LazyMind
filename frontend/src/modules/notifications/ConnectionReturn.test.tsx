import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { TerminalConnectionPage } from '@/modules/channelGateway';
const mocks = vi.hoisted(() => ({ accounts: vi.fn(), create: vi.fn(), cancel: vi.fn(), detail: vi.fn(), refs: vi.fn() }));
vi.mock('@/modules/channelGateway/api', async original => ({ ...await original<typeof import('@/modules/channelGateway/api')>(), listChannelAccounts: mocks.accounts, createConnectionSession: mocks.create, cancelConnectionSession: mocks.cancel }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(), getAccountDetail: mocks.detail, getReferences: mocks.refs }));
vi.mock('react-i18next', async original => { const t = (key: string) => key; return { ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t }) }; });
beforeEach(() => { vi.clearAllMocks(); mocks.accounts.mockResolvedValue({ items: [] }); mocks.cancel.mockResolvedValue(undefined); });
afterEach(cleanup);
it('returns the account from the successful connection only after the user selects it', async () => {
 const account = { id: 'new-account', provider: 'wecom', label: 'New connection', status: 'connected' };
 mocks.detail.mockResolvedValue({ ...account, notification_reference_count: 0 });
 mocks.refs.mockResolvedValue({ items: [], total: 0 });
 mocks.create.mockImplementation(() => {
   mocks.accounts.mockImplementation((provider: string) => Promise.resolve({ items: provider === 'wecom' ? [account] : [] }));
   return Promise.resolve({ id: 'session', provider: 'wecom', mode: 'qr_code', status: 'connected', allowed_actions: [], account });
 });
 const useAccount = vi.fn();
 render(<MemoryRouter><TerminalConnectionPage initialProvider="wecom" embedded onUseAccount={useAccount} /></MemoryRouter>);
 const start = await screen.findByRole('button', { name: /startScan/ });
 await waitFor(() => expect(start).toBeEnabled());
 fireEvent.click(start);
 const action = await screen.findByRole('button', { name: 'notifications.returnUseAccount' });
 expect(useAccount).not.toHaveBeenCalled(); fireEvent.click(action);
 expect(useAccount).toHaveBeenCalledTimes(1); expect(useAccount).toHaveBeenCalledWith(account);
 expect(screen.queryByLabelText('Secret')).not.toBeInTheDocument();
});
