import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ToolRetrievalSetting from './ToolRetrievalSetting';

const api = vi.hoisted(() => ({ getChatSettings: vi.fn(), setToolRetrieval: vi.fn() }));
vi.mock('@/modules/chat/utils/request', () => ({ ConversationSettingsApi: () => api }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

beforeEach(() => {
  vi.clearAllMocks();
  api.getChatSettings.mockResolvedValue({ data: { data: { enable_tool_retrieval: false } } });
  api.setToolRetrieval.mockResolvedValue({});
});

describe('user tool retrieval setting', () => {
  it('loads and saves the user setting without changing conversation defaults', async () => {
    render(<ToolRetrievalSetting />);
    const toggle = screen.getByRole('switch');
    await waitFor(() => expect(toggle).not.toBeDisabled());
    expect(toggle).toHaveAttribute('aria-checked', 'false');
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'true'));
    expect(api.setToolRetrieval).toHaveBeenCalledWith(true);
  });

  it('keeps the saved value when saving fails', async () => {
    api.setToolRetrieval.mockRejectedValue(new Error('offline'));
    render(<ToolRetrievalSetting />);
    const toggle = screen.getByRole('switch');
    await waitFor(() => expect(toggle).not.toBeDisabled());
    fireEvent.click(toggle);
    await screen.findByText('toolRetrieval.error');
    expect(toggle).toHaveAttribute('aria-checked', 'false');
  });
});
