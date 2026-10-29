import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import PaperAttachmentPreview from './PaperAttachmentPreview';
import { libraryFixture } from './__tests__/fixtures';

const { getDocument } = vi.hoisted(() => ({ getDocument: vi.fn() }));
vi.mock('../utils/request', () => ({ DocumentServiceApi: () => ({ documentServiceGetDocument: getDocument }), normalizeProxyableUrl: (url: string) => url }));
vi.mock('../components/FileViewer', () => ({ default: ({ file, fileName }: { file: string; fileName: string }) => <div aria-label="原文件预览" data-file={file}>{fileName}</div> }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

describe('stored attachment preview', () => {
    it('uses the existing file viewer and ignores a response for an attachment that is no longer selected', async () => {
        let finishOld!: (result: { data: { file_url: string } }) => void;
        getDocument.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve; })).mockResolvedValueOnce({ data: { file_url: '/documents/current/file.pdf' } });
        const attachment = { ...libraryFixture().attachments[0], contentRef: 'old-document' };
        const view = render(<PaperAttachmentPreview datasetId="library" attachment={attachment} onPositionChange={vi.fn()} />);
        view.rerender(<PaperAttachmentPreview datasetId="library" attachment={{ ...attachment, contentRef: 'current-document', name: 'Current.pdf' }} onPositionChange={vi.fn()} />);
        expect(await screen.findByLabelText('原文件预览')).toHaveTextContent('Current.pdf');
        await act(async () => finishOld({ data: { file_url: '/documents/old/file.pdf' } }));
        await waitFor(() => expect(screen.getByLabelText('原文件预览')).toHaveAttribute('data-file', `${window.location.origin}/api/core/documents/current/file.pdf`));
        expect(getDocument).toHaveBeenLastCalledWith({ dataset: 'library', document: 'current-document' });
    });
    it('previews the original source when a processed preview is also available', async () => {
        getDocument.mockResolvedValueOnce({data:{file_url:'/processed.pdf',download_file_url:'/source.pdf'}});
        render(<PaperAttachmentPreview datasetId="library" attachment={{...libraryFixture().attachments[0],contentRef:'document',name:'source.pdf'}} onPositionChange={vi.fn()} />);
        expect(await screen.findByLabelText('原文件预览')).toHaveAttribute('data-file', `${window.location.origin}/api/core/source.pdf`);
    });

});
