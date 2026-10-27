import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { extractAcademicReferences, listAcademicReferences, type AcademicReference } from '../api/academicReferences';
import { listExpansions, type Expansion } from '../api/paperWorkflows';
import { libraryFixture, paperFixture } from './__tests__/fixtures';
import PaperDetailDrawer from './PaperDetailDrawer';
vi.mock('./PaperAttachmentPreview', () => ({ default: () => <div>附件预览</div> }));
vi.mock('../api/academicReferences', () => ({ listAcademicReferences: vi.fn(), extractAcademicReferences: vi.fn() }));
vi.mock('../api/paperWorkflows', () => ({ listExpansions: vi.fn() }));
function fixture() {
    const data = libraryFixture({ papers: [
            paperFixture({ id: 'current', academicWorkId: 'work-current', metadata: {
                    title: 'Current paper',
                    identifiers: { arxiv: '2106.09685' }
                } }),
            paperFixture({ id: 'target', academicWorkId: 'work-target', metadata: {
                    title: 'Target paper',
                    identifiers: { doi: '10.1234/target' }
                } }),
            paperFixture({ id: 'incoming', metadata: {
                    title: 'Incoming paper'
                } }),
            paperFixture({ id: 'related', metadata: {
                    title: 'Related paper'
                } }),
        ] });
    data.attachments[0] = { ...data.attachments[0], contentRef: 'pdf-current', sourceVersion: 'v2', importMethod: 'upload' };
    return data;
}
function expansion(): Expansion {
    return {
        id: 'expansion', dataset_id: 'library', conversation_id: 'chat', title: 'Efficient adaptation', goal: 'review', status: 'completed', version: 2,
        preparation_revision: 1, budget_mode: 'balanced',
        created_at: '2026-10-26T00:00:00Z', updated_at: '2026-10-26T00:00:00Z',
        result: { user_instruction: '', candidate_directions: [], selected_directions: ['Adaptation'], papers: [
                { paper: { paper_id: 'work-current', title: 'Current paper', abstract: null }, is_seed: false, is_important: false, seed_relation: 'Builds on the seed method', direction_relation: 'Reduces adaptation cost', key_contribution: 'A compact adaptation method' },
                { paper: { paper_id: 'work-target', title: 'Target paper', abstract: null }, is_seed: true, is_important: false },
            ] },
    };
}
function show(data = fixture(), props: Partial<React.ComponentProps<typeof PaperDetailDrawer>> = {}) {
    return render(<ConfigProvider theme={{ token: { motion: false } }}><PaperDetailDrawer datasetId="library" initialTab="relations" paper={data.papers[0]} data={data} onClose={vi.fn()} onAddNote={vi.fn()} {...props}/></ConfigProvider>);
}
beforeEach(() => {
    vi.mocked(listAcademicReferences).mockReset().mockResolvedValue([]);
    vi.mocked(extractAcademicReferences).mockReset().mockResolvedValue([]);
    vi.mocked(listExpansions).mockReset().mockResolvedValue([]);
});
afterEach(cleanup);
describe('paper relations in the detail drawer', () => {
    it('reports failed reference loading to notifications and resolves it after retry', async () => {
        const notify = vi.fn(); const resolve = vi.fn();
        vi.mocked(listAcademicReferences).mockRejectedValueOnce(new Error('references unavailable'));
        show(fixture(), { onNotification: notify, onNotificationResolved: resolve });
        await waitFor(() => expect(notify).toHaveBeenCalledWith(expect.objectContaining({ id: 'references:current', message: 'references unavailable' })));
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        expect(screen.queryByText('references unavailable')).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '重试' }));
        await waitFor(() => expect(resolve).toHaveBeenCalledWith('references:current'));
    });

    it('merges references from PDF versions and opens the related paper and PDF', async () => {
        const data = fixture();
        data.attachments.push({ ...data.attachments[0], id: 'second-version', contentRef: 'pdf-v1', sourceVersion: 'v1', isPrimary: false });
        data.papers[0].attachmentIds.push('second-version');
        vi.mocked(listAcademicReferences).mockImplementation(async (id) => id === 'pdf-current'
            ? [{ ID: 'ref', ReferenceKey: '1', RawText: 'Author. Target paper. 2024.', DOINormalized: 'https://doi.org/10.1234/TARGET', Page: 8 }]
            : [{ id: 'ref-v1', reference_key: '1', raw_text: 'Author. Target paper. 2024.', resolved_work_id: 'work-target', page: 7 }]);
        const onOpenPaper = vi.fn();
        const onOpenAttachment = vi.fn();
        show(data, { onOpenPaper, onOpenAttachment });
        const citations = screen.getByRole('region', { name: '引用文献' });
        await waitFor(() => expect(within(citations).getByText(/参考文献.*第 7 页/)).toBeInTheDocument());
        expect(within(citations).getAllByRole('article')).toHaveLength(1);
        fireEvent.click(within(citations).getByRole('button', { name: /Target paper/ }));
        expect(onOpenPaper).toHaveBeenCalledWith('target');
        expect(screen.queryByText('全文版本')).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('tab', { name: '附件 2' }));
        const attachments = screen.getByRole('tabpanel', { name: '附件 2' });
        fireEvent.change(within(attachments).getByRole('combobox', { name: '选择预览附件与版本' }), { target: { value: 'second-version' } });
        fireEvent.click(within(attachments).getByRole('button', { name: '阅读 ↗' }));
        expect(onOpenAttachment).toHaveBeenCalledWith('pdf-v1');
        expect(extractAcademicReferences).not.toHaveBeenCalled();
    });
    it('identifies references when needed and presents expansion context separately from citations', async () => {
        vi.mocked(extractAcademicReferences).mockResolvedValue([
            { ID: 'external', Title: 'External method', RawText: 'External method (2025)', ArxivIDBase: '2305.14314v2' },
            ...Array.from({ length: 5 }, (_, index) => ({ ID: `other-${index}`, Title: `Other reference ${index}`, RawText: `Other reference ${index}` })),
        ]);
        vi.mocked(listExpansions).mockResolvedValue([expansion(), { ...expansion(), id: 'unpublished', version: 0, title: 'Unpublished expansion' }]);
        const onOpenPaper = vi.fn();
        show(fixture(), { onOpenPaper });
        expect(await screen.findByRole('link', { name: /External method/ })).toHaveAttribute('href', 'https://arxiv.org/abs/2305.14314');
        expect(extractAcademicReferences).toHaveBeenCalledWith('pdf-current', true);
        const expansionRegion = screen.getByRole('region', { name: '论文扩展' });
        expect(expansionRegion).toHaveTextContent('Builds on the seed method');
        expect(expansionRegion).toHaveTextContent('Reduces adaptation cost');
        expect(expansionRegion).toHaveTextContent('A compact adaptation method');
        const citations = screen.getByRole('region', { name: '引用文献' });
        expect(within(citations).queryByText('Target paper')).not.toBeInTheDocument();
        expect(within(citations).getAllByRole('article')).toHaveLength(5);
        fireEvent.click(within(citations).getByRole('button', { name: '查看全部 6 条参考文献' }));
        expect(within(citations).getAllByRole('article')).toHaveLength(6);
        expect(screen.queryByText('Unpublished expansion')).not.toBeInTheDocument();
        fireEvent.click(within(expansionRegion).getByText('同次扩展的文献'));
        fireEvent.click(within(expansionRegion).getByRole('button', { name: /Target paper/ }));
        expect(onOpenPaper).toHaveBeenCalledWith('target');
    });
    it('retains successful references during a partial failure and retries failed loading', async () => {
        const data = fixture();
        data.attachments.push({ ...data.attachments[0], id: 'second-version', contentRef: 'pdf-v1' });
        data.papers[0].attachmentIds.push('second-version');
        vi.mocked(listAcademicReferences).mockImplementation(async (id) => {
            if (id === 'pdf-v1')
                throw new Error('temporary failure');
            return [{ ID: 'saved', Title: 'Available reference', RawText: 'Available reference' }];
        });
        vi.mocked(listExpansions).mockRejectedValueOnce(new Error('temporary failure')).mockResolvedValue([]);
        show(data);
        expect(await screen.findByText('Available reference')).toBeInTheDocument();
        const citations = screen.getByRole('region', { name: '引用文献' });
        expect(within(citations).getByRole('alert')).toHaveTextContent('暂时无法加载');
        expect(screen.queryByText('暂无已识别的参考文献')).not.toBeInTheDocument();
        vi.mocked(listAcademicReferences).mockResolvedValue([{ ID: 'saved', Title: 'Available reference', RawText: 'Available reference' }]);
        fireEvent.click(within(citations).getByRole('button', { name: /重试/ }));
        fireEvent.click(within(screen.getByRole('region', { name: '论文扩展' })).getByRole('button', { name: /重试/ }));
        await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
        expect(within(citations).getAllByRole('article')).toHaveLength(1);
    });
    it('explains missing PDF data and navigates to attachments without extraction', async () => {
        const data = fixture();
        data.attachments = [];
        data.papers[0].attachmentIds = [];
        show(data);
        expect(await screen.findByText('还没有可识别的 PDF')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: /前往附件/ }));
        expect(screen.getByRole('tab', { name: '附件 0' })).toHaveAttribute('aria-selected', 'true');
        expect(listAcademicReferences).not.toHaveBeenCalled();
        expect(extractAcademicReferences).not.toHaveBeenCalled();
    });
    it('does not apply references returned after navigating to a different paper', async () => {
        const data = fixture();
        data.attachments[1].contentRef = 'pdf-target';
        let finish!: (references: AcademicReference[]) => void;
        vi.mocked(listAcademicReferences).mockImplementation(async (id) => id === 'pdf-current'
            ? new Promise(resolve => { finish = resolve; })
            : [{ ID: 'new', Title: 'New paper reference', RawText: 'New paper reference' }]);
        const { rerender } = show(data);
        await waitFor(() => expect(finish).toBeDefined());
        rerender(<PaperDetailDrawer datasetId="library" initialTab="relations" paper={data.papers[1]} data={data} onClose={vi.fn()} onAddNote={vi.fn()}/>);
        expect(await screen.findByText('New paper reference')).toBeInTheDocument();
        await act(async () => finish([{ ID: 'obsolete', Title: 'Obsolete reference', RawText: 'Obsolete reference' }]));
        expect(screen.queryByText('Obsolete reference')).not.toBeInTheDocument();
    });
});
