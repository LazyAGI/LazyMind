import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import PaperBackupRestore from './PaperBackupRestore';
import { libraryFixture } from './__tests__/fixtures';
import { serializeLibraryBackup } from './libraryBackup';

afterEach(cleanup);
it('previews the backup and restores metadata without cross-library PDF references', async () => {
    const restore = vi.fn().mockResolvedValue(undefined), close = vi.fn();
    render(<PaperBackupRestore open datasetId="destination" disabled={false} onCancel={close} onRestore={restore} />);
    expect(screen.getByRole('button', { name: /确认恢复/ })).toBeDisabled();
    const data = libraryFixture();
    data.attachments[0].contentRef = 'origin-document';
    const file = new File([serializeLibraryBackup(data, 'origin')], 'library.json', { type: 'application/json' });
    fireEvent.change(screen.getByLabelText('文献库备份文件'), { target: { files: [file] } });
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('1 篇文献'));
    expect(screen.getByRole('checkbox')).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: /确认恢复/ }));
    await waitFor(() => expect(restore).toHaveBeenCalledOnce());
    expect(restore.mock.calls[0][0].attachments[0].contentRef).toBeUndefined();
    expect(restore.mock.calls[0][0].papers).toEqual(data.papers);
    expect(close).toHaveBeenCalledOnce();
});
it('keeps invalid backups visible as errors without submitting a restore', async () => {
    const restore = vi.fn();
    render(<PaperBackupRestore open datasetId="same" disabled={false} onCancel={vi.fn()} onRestore={restore} />);
    fireEvent.change(screen.getByLabelText('文献库备份文件'), { target: { files: [new File(['{}'], 'invalid.json')] } });
    expect(await screen.findByRole('alert')).toHaveTextContent('不是有效的文献库备份');
    expect(screen.getByRole('button', { name: /确认恢复/ })).toBeDisabled();
    expect(restore).not.toHaveBeenCalled();
});
