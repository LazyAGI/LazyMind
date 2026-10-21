import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import MailReader from './MailReader';
import { readDirectoryMail, type DirectoryMail, type DocumentDirectoryEntry } from './api';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('./api', () => ({ readDirectoryMail: vi.fn() }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => 'Read failed' }));
const entry = { id: 'one', title: 'One' } as DocumentDirectoryEntry;
const mail = { subject: 'Original', from: 'sender', to: 'recipient', cc: '', date: '', mailbox: 'account', body: 'Correct body', attachments: [], truncated: false } as DirectoryMail;
beforeEach(() => vi.clearAllMocks());

it('ignores a previous mail response after switching and after closing', async () => {
  let resolveFirst!: (value: DirectoryMail) => void;
  vi.mocked(readDirectoryMail).mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve; })).mockResolvedValueOnce(mail);
  const { rerender } = render(<MailReader directoryId="d" entry={entry} onClose={() => {}} />);
  rerender(<MailReader directoryId="d" entry={{ ...entry, id: 'two' }} onClose={() => {}} />);
  expect(await screen.findByText('Correct body')).toBeInTheDocument();
  await act(async () => resolveFirst({ ...mail, body: 'Stale body' }));
  expect(screen.queryByText('Stale body')).not.toBeInTheDocument();
  expect(readDirectoryMail).toHaveBeenCalledTimes(2);
  const signal = vi.mocked(readDirectoryMail).mock.calls[1][2];
  rerender(<MailReader directoryId="d" onClose={() => {}} />);
  expect(signal?.aborted).toBe(true);
});

it('allows retry and renders returned body as text', async () => {
  vi.mocked(readDirectoryMail).mockRejectedValueOnce(new Error('failed')).mockResolvedValueOnce({ ...mail, body: '<script>untrusted</script>' });
  render(<MailReader directoryId="d" entry={entry} onClose={() => {}} />);
  expect(await screen.findByText('Read failed')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'directories.retry' }));
  await waitFor(() => expect(screen.getByText('<script>untrusted</script>')).toBeInTheDocument());
  expect(document.querySelector('.directory-mail-body script')).toBeNull();
});
