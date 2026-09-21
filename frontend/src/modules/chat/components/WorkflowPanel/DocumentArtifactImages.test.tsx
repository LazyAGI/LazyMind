import { useEffect, useState } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useWorkflowStore, type SlotRevision, type WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { DocumentArtifactEditor } from './DocumentArtifactEditor';

const api = vi.hoisted(() => ({ renderWriterDocument: vi.fn(), saveDocumentArtifact: vi.fn() }));
vi.mock('@/modules/chat/utils/request', async (original) => ({
  ...await original<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => api,
}));
vi.mock('./useDocumentCopy', () => ({ useDocumentCopy: () => {} }));
vi.mock('./useWriterProviderAvailability', () => ({ useWriterProviderAvailability: () => ({ states: {} }) }));
vi.mock('./DocumentPublicationRecoveryPanel', () => ({ DocumentPublicationRecoveryPanel: () => null }));
vi.mock('./WriterIRControl', () => ({ WriterIRControl: () => <article>IR document</article> }));
vi.mock('./ArtifactRewriteDialog', () => ({ ArtifactRewriteDialog: () => null }));
vi.mock('./MarkdownArtifactEditor', () => ({
  MarkdownArtifactEditor: ({ markdown, resolveImageUrl, onSave, sourceRevision }: {
    markdown: string;
    resolveImageUrl: (source: string) => Promise<string>;
    onSave: (text: string, revision: number, mode: 'draft') => Promise<unknown>;
    sourceRevision: number;
  }) => {
    const [src, setSrc] = useState('');
    const [text, setText] = useState(markdown);
    useEffect(() => {
      const source = /!\[[^\]]*\]\(([^)]+)\)/.exec(markdown)?.[1];
      // MDXEditor's image preview handler can finish after a newer handler call.
      if (source) void resolveImageUrl(source).then(setSrc);
    }, [markdown, resolveImageUrl]);
    return <>
      <article>{markdown}</article><img alt='Document illustration' src={src || undefined} />
      <textarea aria-label='Document content' value={text} onChange={e => setText(e.target.value)} />
      <button onClick={() => void onSave(text, sourceRevision, 'draft')}>Save document</button>
    </>;
  },
}));

const source = 'docs/assets/knowledge-library-en.jpg';
const markdown = `# README\n\n![Knowledge library](${source})`;
const slot = {
  artifact_id: 'source-artifact', slot_id: 'source_document', revision: 1, selected: true,
  artifact_value: { text: markdown },
  document: { representation: 'markdown', editable: true, capabilities: ['save'] },
} as SlotRevision;

function session(id = 'session', revision = 1): WorkflowSession {
  return { session_id: id, workflow_id: 'writer-workflow', slots: [
    { slot_id: 'media_assets', artifact_id: `media-${revision}`, revision, selected: true },
  ] } as WorkflowSession;
}
function response(url?: string) {
  return { data: { code: 0, data: { representation: 'markdown', document: 'Server rendering must not replace the editor',
    media_urls: url ? { [source]: url } : {} } } };
}
function pending<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

beforeEach(() => {
  api.renderWriterDocument.mockReset();
  api.saveDocumentArtifact.mockReset();
  useWorkflowStore.setState({ sessionByConversation: { conversation: session() } });
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ urls: {} }) })));
});
afterEach(() => { cleanup(); useWorkflowStore.setState({ sessionByConversation: {} }); vi.unstubAllGlobals(); });

describe('document artifact image previews', () => {
  it('resolves downloaded media for a read-only source while retaining canonical Markdown', async () => {
    api.renderWriterDocument.mockResolvedValue(response('https://example.test/preview.jpg'));
    render(<DocumentArtifactEditor slot={slot} sessionId='session' readOnly />);
    await waitFor(() => expect(screen.getByRole('img')).toHaveAttribute('src', 'https://example.test/preview.jpg'));
    expect(screen.getByRole('article').textContent).toBe(markdown);
    expect(api.renderWriterDocument).toHaveBeenCalledWith('session', 'source_document', expect.any(Object));
    expect(api.saveDocumentArtifact).not.toHaveBeenCalled();
  });

  it('keeps editing and saves original image references while previews are loading', async () => {
    const request = pending<ReturnType<typeof response>>();
    api.renderWriterDocument.mockReturnValue(request.promise);
    api.saveDocumentArtifact.mockResolvedValue({ data: { ok: true, result: {
      artifact_id: 'saved', revision: 2, draft_version: 1,
    } } });
    render(<DocumentArtifactEditor slot={slot} sessionId='session' />);
    const edited = `${markdown}\n\nUser edits`;
    fireEvent.change(await screen.findByLabelText('Document content'), { target: { value: edited } });
    await act(async () => request.resolve(response('https://example.test/preview.jpg')));
    await waitFor(() => expect(screen.getByRole('img')).toHaveAttribute('src', 'https://example.test/preview.jpg'));
    expect(screen.getByLabelText('Document content')).toHaveValue(edited);
    fireEvent.click(screen.getByText('Save document'));
    await waitFor(() => expect(api.saveDocumentArtifact).toHaveBeenCalledWith('source-artifact',
      expect.objectContaining({ value: { text: edited } }), expect.any(Object)));
  });

  it('refreshes previews when media arrives without changing the document revision', async () => {
    api.renderWriterDocument.mockResolvedValueOnce(response()).mockResolvedValueOnce(response('https://example.test/ready.jpg'));
    render(<DocumentArtifactEditor slot={slot} sessionId='session' />);
    await waitFor(() => expect(api.renderWriterDocument).toHaveBeenCalledTimes(1));
    act(() => useWorkflowStore.setState({ sessionByConversation: { conversation: session('session', 2) } }));
    await waitFor(() => expect(screen.getByRole('img')).toHaveAttribute('src', 'https://example.test/ready.jpg'));
    expect(screen.getByRole('article').textContent).toBe(markdown);
  });

  it('does not let a late fallback lookup overwrite a resolved media preview', async () => {
    const media = pending<ReturnType<typeof response>>();
    const signing = pending<{ ok: boolean; json: () => Promise<{ urls: Record<string, string> }> }>();
    api.renderWriterDocument.mockReturnValue(media.promise);
    vi.mocked(fetch).mockReturnValue(signing.promise as Promise<Response>);
    render(<DocumentArtifactEditor slot={slot} sessionId='session' />);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    await act(async () => media.resolve(response('https://example.test/ready.jpg')));
    await waitFor(() => expect(screen.getByRole('img')).toHaveAttribute('src', 'https://example.test/ready.jpg'));
    await act(async () => signing.resolve({ ok: true, json: async () => ({ urls: {} }) }));
    expect(screen.getByRole('img')).toHaveAttribute('src', 'https://example.test/ready.jpg');
  });

  it('ignores a late response after the user switches documents', async () => {
    const old = pending<ReturnType<typeof response>>();
    api.renderWriterDocument.mockReturnValueOnce(old.promise).mockResolvedValueOnce(response('https://example.test/new.jpg'));
    useWorkflowStore.setState({ sessionByConversation: { first: session(), second: session('second') } });
    const view = render(<DocumentArtifactEditor slot={slot} sessionId='session' />);
    await waitFor(() => expect(api.renderWriterDocument).toHaveBeenCalledTimes(1));
    view.rerender(<DocumentArtifactEditor slot={{ ...slot, artifact_id: 'second-artifact' }} sessionId='second' />);
    await waitFor(() => expect(screen.getByRole('img')).toHaveAttribute('src', 'https://example.test/new.jpg'));
    await act(async () => old.resolve(response('https://example.test/old.jpg')));
    expect(screen.getByRole('img')).toHaveAttribute('src', 'https://example.test/new.jpg');
  });

  it('keeps the body and ordinary image URLs available when media lookup fails', async () => {
    api.renderWriterDocument.mockRejectedValue(new Error('Media unavailable'));
    const text = '# Body\n\n![Remote](https://example.test/direct.jpg)';
    render(<DocumentArtifactEditor slot={{ ...slot, artifact_value: { text } }} sessionId='session' />);
    await waitFor(() => expect(screen.getByRole('img')).toHaveAttribute('src', 'https://example.test/direct.jpg'));
    expect(screen.getByRole('article').textContent).toBe(text);
    expect(screen.getByLabelText('Document content')).toBeEnabled();
  });

  it('does not call Writer rendering for a different workflow', async () => {
    useWorkflowStore.setState({ sessionByConversation: { conversation: { ...session(), workflow_id: 'other-workflow' } } });
    render(<DocumentArtifactEditor slot={slot} sessionId='session' />);
    await screen.findByRole('article');
    expect(api.renderWriterDocument).not.toHaveBeenCalled();
  });
});
