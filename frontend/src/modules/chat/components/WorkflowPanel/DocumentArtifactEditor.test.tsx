const availability = vi.hoisted(() => ({ states: { feishu: 'ready', github: 'ready', wechat: 'ready', obsidian: 'ready', notion: 'ready' }, refresh: vi.fn() }));
vi.mock('./useWriterProviderAvailability', () => ({ useWriterProviderAvailability: () => availability }));
import { act, fireEvent, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { DocumentArtifactEditor } from './DocumentArtifactEditor';
import { SlotEditingContext, type SlotFooterAction } from './slotEditingContext';

const api = vi.hoisted(() => ({ getDocumentArtifact: vi.fn(), convertDocument: vi.fn(), saveDocumentArtifact: vi.fn(), previewDocumentAction: vi.fn(), listDocumentProviders: vi.fn(), publishDocument: vi.fn(), getPublicationForArtifact: vi.fn(async()=>({data:{data:{}}})) }));
const download = vi.hoisted(() => vi.fn());
const confirm = vi.hoisted(() => vi.fn());
vi.mock('@/modules/chat/utils/request', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => api,
}));
vi.mock('antd', async (importOriginal) => ({
  ...await importOriginal<typeof import('antd')>(), Modal: { confirm },
}));

vi.mock('./MarkdownArtifactEditor', () => ({
  MarkdownArtifactEditor: ({ markdown, sourceRevision, onSave }: { markdown: string; sourceRevision: number; onSave: (text: string, revision: number, mode: 'draft') => Promise<unknown> }) => <>
    <article>{markdown}</article>
    <button onClick={() => void onSave('# Updated draft', sourceRevision, 'draft')}>save draft fixture</button>
  </>,
}));
vi.mock('./WriterIRControl', () => ({ WriterIRControl: () => null }));
vi.mock('./ArtifactRewriteDialog', () => ({ ArtifactRewriteDialog: () => null }));
vi.mock('./WriterDownloadFormat', () => ({
  downloadSource: download,
  WriterDownloadFormatDialog: () => null,
  writerDownloadFilename: () => '',
  writerDownloadCacheKey: () => '',
}));

afterEach(() => { cleanup(); vi.clearAllMocks(); availability.refresh.mockReset(); availability.states = { feishu: 'ready', github: 'ready', wechat: 'ready', obsidian: 'ready', notion: 'ready' }; });

it('keeps the edited draft while linking directly to Feishu settings, then publishes the same draft', async () => {
  await i18n.changeLanguage('zh-CN');
  availability.states = { ...availability.states, feishu: 'chat-disabled' };
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu' }] } } });
  api.saveDocumentArtifact.mockResolvedValue({ data: { ok: true, result: {
    artifact_id: 'retained-draft', revision: 3, draft_version: 8, value: { text: '# Updated draft' },
  } } });
  api.publishDocument.mockResolvedValue({ data: { data: {
    artifact_id: 'retained-draft', revision: 4, draft_version: 8, document: '# Updated draft', provider_synced: true, artifact_saved: true,
  } } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'retained-draft', slot_id: 'draft_document', revision: 3, draft_version: 7,
    artifact_value: { text: '# Completed draft' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  const view = () => <SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider>;
  const { rerender } = render(view());
  await waitFor(() => expect(action?.disabled).toBe(false));
  fireEvent.click(screen.getByRole('button', { name: 'save draft fixture' }));
  await waitFor(() => expect(screen.getByRole('article')).toHaveTextContent('# Updated draft'));
  act(() => action!.onClick());
  expect(screen.getByRole('alert')).toHaveTextContent('当前飞书参与对话未开启，请点击跳转设置');
  const settings = screen.getByRole('link', { name: '前往设置' });
  expect(settings).toHaveAttribute('href', `${window.location.origin}/cloud-documents/feishu`);
  expect(settings).toHaveAttribute('target', '_blank');
  expect(api.publishDocument).not.toHaveBeenCalled();
  expect(action?.flushBeforeAction).toBe(true);
  availability.states = { ...availability.states, feishu: 'ready' };
  rerender(view());
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  expect(screen.getByRole('article')).toHaveTextContent('# Updated draft');
  expect(api.publishDocument).not.toHaveBeenCalled();
  await act(async () => action!.onClick());
  expect(api.publishDocument).toHaveBeenCalledWith('retained-draft', expect.objectContaining({
    base_revision: 3, base_draft_version: 8, input: expect.objectContaining({ provider: 'feishu' }),
  }), expect.anything());
});

it('copies LaTeX using the saved draft version before a parent refresh', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
  api.convertDocument.mockResolvedValue({ data: { code: 0, data: { format: 'latex', content: 'converted fixture' } } });
  api.saveDocumentArtifact.mockResolvedValue({ data: { ok: true, result: {
    artifact_id: 'copy-fixture', revision: 3, draft_version: 8, value: { text: '# Updated draft' },
  } } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'copy-fixture', slot_id: 'draft_document', revision: 3, draft_version: 7,
    artifact_value: { text: '# Draft' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'convert_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'copy') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='session' /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action).toBeDefined());
  act(() => action!.menu!.find(item => item.key === 'latex')!.onClick());
  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
  expect(api.convertDocument).toHaveBeenLastCalledWith('session', 'draft_document', -1, 3, 'latex', '# Draft', 7);
  fireEvent.click(screen.getByRole('button', { name: 'save draft fixture' }));
  await waitFor(() => expect(screen.getByRole('article')).toHaveTextContent('# Updated draft'));
  act(() => action!.onClick());
  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(2));
  expect(api.convertDocument).toHaveBeenLastCalledWith('session', 'draft_document', -1, 3, 'latex', '# Updated draft', 8);
});

describe('document artifact display', () => {
  it('shows the Feishu settings action if chat is disabled after the initial availability check', async () => {
    await i18n.changeLanguage('zh-CN');
    api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu' }] } } });
    api.publishDocument.mockRejectedValue({ response: { data: { data: { code: 'PROVIDER_CREDENTIALS_UNAVAILABLE' } } } });
    availability.refresh.mockImplementation(async () => {
      availability.states = { ...availability.states, feishu: 'chat-disabled' };
      return availability.states;
    });
    let action: SlotFooterAction | undefined;
    const slot = { artifact_id: 'permission-changed', slot_id: 'draft_document', revision: 1,
      artifact_value: { text: '# Completed draft' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
    render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
      registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
    }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider>);
    await waitFor(() => expect(action?.disabled).toBe(false));
    await act(async () => action!.onClick());
    expect(screen.getByRole('alert')).toHaveTextContent('当前飞书参与对话未开启，请点击跳转设置');
    expect(screen.getByRole('link', { name: '前往设置' })).toHaveAttribute('href', `${window.location.origin}/cloud-documents/feishu`);
    expect(screen.getByRole('article')).toHaveTextContent('# Completed draft');
    expect(api.publishDocument).toHaveBeenCalledTimes(1);
  });
  it('shows the failed publication stage returned by the backend', async () => {
    await i18n.changeLanguage('zh-CN');
    api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu', capabilities: ['create', 'replace'] }] } } });
    api.publishDocument.mockRejectedValue({ response: { data: { data: { code: 'DOCUMENT_CONVERSION_FAILED' } } } });
    const registerFooterAction = vi.fn((_key: string, _action: SlotFooterAction | null) => () => {});
    const slot = {
      artifact_id: 'artifact-test', slot_id: 'draft_document', revision: 1,
      artifact_value: { text: '# Completed story' },
      document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] },
    } as SlotRevision;
    render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {}, registerFooterAction }}>
      <DocumentArtifactEditor slot={slot} sessionId='session-test' />
    </SlotEditingContext.Provider>);
    await waitFor(() => expect(registerFooterAction).toHaveBeenCalledWith(
      expect.any(String), expect.objectContaining({ icon: 'write-back', disabled: false }),
    ));
    const action = registerFooterAction.mock.calls.at(-1)![1]!;
    await act(async () => action.onClick());
    expect(confirm).not.toHaveBeenCalled();
    expect(await screen.findByRole('alert')).toHaveTextContent('飞书 文档转换阶段失败，尚未写入');
    expect(api.publishDocument).toHaveBeenCalledTimes(1);
  });
  it.each([['zh-CN', '下载'], ['en-US', 'Download']])(
    'loads the completed Markdown and registers a translated download label in %s',
    async (language, label) => {
      await i18n.changeLanguage(language);
      const registerFooterAction = vi.fn(() => () => {});
      const slot = {
        artifact_id: 'artifact-test', slot_id: 'draft_document', revision: 1,
        artifact_value: { text: '# Completed story\n\nThe first paragraph.' },
        document: { representation: 'markdown', editable: true, capabilities: ['save', 'convert_document'] },
      } as SlotRevision;
      render(
        <SlotEditingContext.Provider value={{
          setEditing: vi.fn(), registerFlush: () => () => {}, registerFooterAction,
        }}>
          <DocumentArtifactEditor slot={slot} sessionId='session-test' />
        </SlotEditingContext.Provider>,
      );
      expect(await screen.findByRole('article')).toHaveTextContent('The first paragraph.');
      await waitFor(() => expect(registerFooterAction).toHaveBeenCalledWith(
        expect.any(String), expect.objectContaining({ icon: 'download', label }),
      ));
    },
  );
});
it('downloads the click-time draft and retains that snapshot for retry without saving', async () => {
  await i18n.changeLanguage('zh-CN');
  let snapshot = '# 当前未保存草稿';
  let finish!: (value: unknown) => void;
  let downloadAction: SlotFooterAction | undefined;
  api.previewDocumentAction.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }))
    .mockResolvedValueOnce({ data: { data: { content: 'Converted original snapshot' } } });
  download.mockRejectedValueOnce(new Error('download failed')).mockResolvedValueOnce(undefined);
  const slot = { artifact_id: 'download-fixture', slot_id: 'draft_document', revision: 3, draft_version: 7,
    artifact_value: { text: '# 保存的旧稿' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'convert_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {}, getSnapshot: () => snapshot,
    registerFooterAction: (_key, action) => { if (action?.icon === 'download') downloadAction = action; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider>);
  await waitFor(() => expect(downloadAction).toBeDefined());
  act(() => downloadAction!.menu!.find(item => item.key === 'text')!.onClick());
  expect(api.previewDocumentAction.mock.calls[0][1].input).toEqual({ output_format: 'text', document: '# 当前未保存草稿' });
  snapshot = '# 下载过程中新增的文字';
  await act(async () => finish({ data: { data: { content: 'Converted original snapshot' } } }));
  expect(screen.getByRole('alert')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '重试' }));
  await waitFor(() => expect(download).toHaveBeenCalledTimes(2));
  expect(api.previewDocumentAction.mock.calls[1][1].input.document).toBe('# 当前未保存草稿');
  expect(download.mock.calls[1][0].content).toBe('Converted original snapshot');
});
it('confirms the existing publication target before an overwrite', async () => {
  await i18n.changeLanguage('zh-CN');
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu' }] } } });
  api.publishDocument.mockResolvedValue({ data: { data: { artifact_id: 'saved', revision: 2, draft_version: 1, document: '# Draft', provider_synced: true, artifact_saved: true } } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'bound', slot_id: 'draft_document', revision: 1, provider: 'feishu', provider_document_id: 'existing-target', write_back_ready: true,
    write_back_url: 'https://example.test/document', artifact_value: { text: '# Draft' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {}, registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; } }}>
    <DocumentArtifactEditor slot={slot} sessionId='fixture' />
  </SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.disabled).toBe(false));
  act(() => action!.onClick());
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(api.publishDocument).not.toHaveBeenCalled();
  await act(async () => confirm.mock.calls[0][0].onOk());
  expect(api.publishDocument).toHaveBeenCalledTimes(1);
});

it.each([
  ['feishu', { uri: 'https://example.test/published-document' }, 'https://example.test/published-document'],
  ['feishu', { uri: 'feishu:/~docx/FixtureDocumentToken', doc_id: 'FixtureDocumentToken' }, 'https://feishu.cn/docx/FixtureDocumentToken'],
  ['feishu', { uri: 'feishu@FixtureSpace:/~node/FixtureWikiToken', doc_id: 'FixtureDocumentToken' }, 'https://feishu.cn/wiki/FixtureWikiToken'],
  ['feishu', { doc_id: 'FixtureDocumentToken' }, 'https://feishu.cn/docx/FixtureDocumentToken'],
  ['notion', { uri: 'notion:/~page/12345678-1234-1234-1234-123456789abc', doc_id: '12345678-1234-1234-1234-123456789abc' }, 'https://www.notion.so/12345678123412341234123456789abc'],
  ['feishu', { uri: 'https://example.feishu.cn/wiki/fixture-document' }, 'https://example.feishu.cn/wiki/fixture-document'],
  ['notion', { uri: 'notion:/~page/fixture', meta: { browser_url: 'https://www.notion.so/fixture-document' } }, 'https://www.notion.so/fixture-document'],
  ['github', { uri: 'github://repo/note.md', meta: { browser_url: 'https://github.com/example/docs/blob/main/note.md' } }, 'https://github.com/example/docs/blob/main/note.md'],
  ['github', { uri: 'github://repo/note.md', meta: { browser_url: 'https://github.com/example/docs/blob/main/note.md', pull_request_url: 'https://github.com/example/docs/pull/7' } }, 'https://github.com/example/docs/pull/7'],
  ['github', { uri: 'githubwiki://example/docs/note', meta: { browser_url: 'https://github.com/example/docs/wiki/Note' } }, 'https://github.com/example/docs/wiki/Note'],
  ['wechat', { uri: 'wechat://draft/fixture', meta: { browser_url: 'https://mp.weixin.qq.com/s?tempkey=fixture-preview' } }, 'https://mp.weixin.qq.com/s?tempkey=fixture-preview'],
  ['obsidian', { uri: 'obsidian://vlt_fixture/note.md', meta: { local_path: '/Users/test/My Vault/中文 #1.md' } }, 'obsidian://open?path=%2FUsers%2Ftest%2FMy%20Vault%2F%E4%B8%AD%E6%96%87%20%231.md'],
])('shows the returned %s document link in the footer immediately after publication', async (provider, target, url) => {
  await i18n.changeLanguage('zh-CN');
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: provider }] } } });
  api.publishDocument.mockResolvedValue({ data: { data: { artifact_id: 'saved', revision: 2, draft_version: 1,
    document: '# Draft', provider_synced: true, artifact_saved: true, target_document: { adapter: provider, ...target } } } });
  let action: SlotFooterAction | undefined;
  const onRefresh = vi.fn();
  const slot = { artifact_id: 'publication-link', slot_id: 'draft_document', revision: 1, artifact_value: { text: '# Draft' },
    document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' onRefresh={onRefresh} /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.disabled).toBe(false));
  await act(async () => action!.onClick());
  await waitFor(() => expect(action?.statusLink).toEqual({ href: url, label: '打开云文档' }));
  expect(action?.statusText).toBe('已写回云文档');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(confirm).not.toHaveBeenCalled();
  expect(onRefresh).toHaveBeenCalledTimes(1);
  expect(api.publishDocument).toHaveBeenCalledTimes(1);
});

it.each(['success', 'failure'])('shows the requested platform throughout publication and clears it after %s', async outcome => {
  await i18n.changeLanguage('zh-CN');
  localStorage.clear();
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu' }, { id: 'notion' }, { id: 'github' }] } } });
  let finish!: (response: unknown) => void;
  let fail!: (error: unknown) => void;
  api.publishDocument.mockImplementation(() => new Promise((resolve, reject) => { finish = resolve; fail = reject; }));
  let action: SlotFooterAction | undefined;
  const onRefresh = vi.fn();
  const slot = { artifact_id: 'feishu-bound', slot_id: 'draft_document', revision: 1, draft_version: 1,
    provider: 'feishu', provider_document_id: 'feishu-document', write_back_ready: true,
    artifact_value: { text: '# Draft' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' onRefresh={onRefresh} /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.disabled).toBe(false));
  act(() => action!.menu!.find(item => item.key === 'notion')!.onClick());
  expect(api.publishDocument).toHaveBeenCalledWith('feishu-bound', expect.objectContaining({ input: expect.objectContaining({ provider: 'notion' }) }), expect.anything());
  expect(action?.label).toBe('正在写入Notion…');
  expect(action?.disabled).toBe(true);
  const success = { data: { data: { artifact_id: 'notion-published', revision: 2, draft_version: 1,
    provider_synced: true, artifact_saved: true, document: '# Draft', target_document: { uri: 'https://example.test/notion' } } } };
  await act(async () => {
    if (outcome === 'success') finish(success);
    else fail({ response: { data: { data: { code: 'DOCUMENT_CONVERSION_FAILED' } } } });
  });
  await waitFor(() => expect(action?.disabled).toBe(false));
  expect(action?.label).toBe('发布');
  expect(onRefresh).toHaveBeenCalledTimes(outcome === 'success' ? 1 : 0);
  if (outcome === 'failure') expect(action?.statusText).toContain('Notion');
  act(() => action!.menu!.find(item => item.key === 'github')!.onClick());
  expect(action?.label).toBe('正在写入GitHub…');
  expect(api.publishDocument.mock.calls[1][1].input.provider).toBe('github');
  await act(async () => finish(success));
  await waitFor(() => expect(action?.disabled).toBe(false));
  expect(action?.label).toBe('发布');
  expect(confirm).not.toHaveBeenCalled();
});

it.each([
  ['feishu', 'feishu:/~docx/FixtureDoc', 'FixtureDoc', 'https://feishu.cn/docx/FixtureDoc'],
  ['feishu', undefined, 'FixtureDoc', 'https://feishu.cn/docx/FixtureDoc'],
  ['notion', 'notion:/~page/12345678-1234-1234-1234-123456789abc', '12345678-1234-1234-1234-123456789abc', 'https://www.notion.so/12345678123412341234123456789abc'],
])('restores the existing %s slot target without publishing again', async (provider, uri, id, expected) => {
  await i18n.changeLanguage('zh-CN');
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: provider }] } } });
  api.getPublicationForArtifact.mockResolvedValueOnce({ data: { data: {} } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'existing-binding', slot_id: 'draft_document', revision: 1, provider, provider_document_id: id,
    write_back_url: uri, artifact_value: { text: '# Draft' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.statusLink?.href).toBe(expected));
  expect(api.publishDocument).not.toHaveBeenCalled();
});

it.each(['https://example.test/published-document', 'https://github.com/example/docs/pull/7', 'https://mp.weixin.qq.com/', 'obsidian://open?path=%2FUsers%2Ftest%2FVault%2Fnote.md', 'javascript:alert(1)', 'https://user:password@example.test/document'])('restores a safe success link without a recovery banner: %s', async url => {
  await i18n.changeLanguage('zh-CN');
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu' }] } } });
  api.getPublicationForArtifact.mockResolvedValueOnce({ data: { data: { operation: { operation_id: 'completed-operation', status: 'succeeded',
    provider: 'feishu', provider_synced: true, artifact_id: 'publication-link', source_slot_id: 'draft_document', item_index: -1,
    updated_at: '2026-09-15T00:00:00Z', target_url: url, actions: [] } } } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'publication-link', slot_id: 'draft_document', revision: 1, artifact_value: { text: '# Draft' },
    document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.disabled).toBe(false));
  expect(action?.statusLink).toEqual(url.includes('javascript:') || url.includes('password@') ? undefined : { href: url, label: '打开云文档' });
  expect(action?.statusText).toBe('已写回云文档');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(screen.queryByText('上次云文档发布')).not.toBeInTheDocument();
  expect(api.publishDocument).not.toHaveBeenCalled();
});

it.each(['outcome_unknown_released', 'confirmed_detached', 'outcome_unknown'])('keeps publication state below the editor and the target link in the footer: %s', async status => {
  await i18n.changeLanguage('zh-CN');
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu' }] } } });
  api.getPublicationForArtifact.mockResolvedValueOnce({ data: { data: { operation: { operation_id: 'publication-fixture', status,
    provider: 'feishu', source_slot_id: 'draft_document', item_index: -1, updated_at: '2026-09-15T00:00:00Z',
    target_url: 'https://example.test/document', actions: [] } } } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'publication-fixture', slot_id: 'draft_document', revision: 1, artifact_value: { text: '# Draft' },
    document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.statusLink?.href).toBe('https://example.test/document'));
  expect(action?.statusText).not.toBe('已写回云文档');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(screen.queryByText('上次云文档发布')).not.toBeInTheDocument();
  if (status === 'outcome_unknown') {
    const toggle = screen.getByRole('button', { name: '发布状态' });
    expect(screen.getByRole('article').compareDocumentPosition(toggle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(action?.disabled).toBe(true);
  } else expect(screen.queryByRole('button', { name: '发布状态' })).not.toBeInTheDocument();
  expect(api.publishDocument).not.toHaveBeenCalled();
});

it('blocks stale publication buttons and menus until the refreshed draft is loaded', async () => {
 await i18n.changeLanguage('zh-CN');
 api.listDocumentProviders.mockResolvedValue({data:{data:{providers:[{id:'feishu'},{id:'obsidian'}]}}});
 api.getPublicationForArtifact.mockResolvedValue({data:{data:{operation:{operation_id:'op',status:'succeeded',artifact_id:'latest',provider:'feishu',source_slot_id:'draft_document',item_index:-1,actions:[]}}}});
 api.getDocumentArtifact.mockResolvedValue({data:{ok:true,result:{artifact_id:'stale',selected:false}}});
 api.publishDocument.mockResolvedValue({data:{data:{artifact_id:'next',revision:4,draft_version:1,document:'# Latest',provider_synced:true,artifact_saved:true}}});
 let action: SlotFooterAction | undefined;
 const slot={artifact_id:'stale',slot_id:'draft_document',revision:2,draft_version:1,artifact_value:{text:'# Stale'},document:{representation:'markdown',editable:true,capabilities:['save','publish_document']}} as SlotRevision;
 const onRefresh=vi.fn();
 const context={setEditing:vi.fn(),registerFlush:()=>()=>{},registerFooterAction:(_key:string,next:SlotFooterAction|null)=>{if(next?.icon==='write-back')action=next;return ()=>{};}};
 const view=(current:SlotRevision)=><SlotEditingContext.Provider value={context}><DocumentArtifactEditor slot={current} sessionId='fixture' onRefresh={onRefresh}/></SlotEditingContext.Provider>;
 const rendered=render(view(slot));
 await screen.findByRole('button',{name:'更新成稿'});
 await waitFor(()=>expect(action?.disabled).toBe(true));
 await act(async()=>{action!.onClick();action!.menu!.find(item=>item.key==='obsidian')!.onClick();});
 expect(api.publishDocument).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'更新成稿'}));
 expect(onRefresh).toHaveBeenCalledTimes(1);
 rendered.rerender(view({...slot,artifact_id:'latest',revision:3,draft_version:7,artifact_value:{text:'# Latest'}}));
 await waitFor(()=>expect(action?.disabled).toBe(false));
 await act(async()=>action!.menu!.find(item=>item.key==='obsidian')!.onClick());
 expect(api.publishDocument).toHaveBeenCalledWith('latest',expect.objectContaining({base_revision:3,base_draft_version:7,input:expect.objectContaining({provider:'obsidian'})}),expect.anything());
 api.getPublicationForArtifact.mockResolvedValue({data:{data:{}}});
});
it('retains the accepted publication version through a delayed older parent snapshot', async () => {
 api.getPublicationForArtifact.mockResolvedValue({data:{data:{}}});
 api.listDocumentProviders.mockResolvedValue({data:{data:{providers:[{id:'feishu'},{id:'obsidian'}]}}});
 api.publishDocument.mockResolvedValue({data:{data:{artifact_id:'published',revision:3,draft_version:1,document:'# Published',provider_synced:true,artifact_saved:true}}});
 let action: SlotFooterAction | undefined;
 const slot={artifact_id:'initial',slot_id:'draft_document',revision:1,draft_version:1,artifact_value:{text:'# Initial'},document:{representation:'markdown',editable:true,capabilities:['save','publish_document']}} as SlotRevision;
 const context={setEditing:vi.fn(),registerFlush:()=>()=>{},registerFooterAction:(_key:string,next:SlotFooterAction|null)=>{if(next?.icon==='write-back')action=next;return ()=>{};}};
 const view=(current:SlotRevision)=><SlotEditingContext.Provider value={context}><DocumentArtifactEditor slot={current} sessionId='fixture'/></SlotEditingContext.Provider>;
 const rendered=render(view(slot));
 await waitFor(()=>expect(action?.disabled).toBe(false));
 await act(async()=>action!.menu!.find(item=>item.key==='feishu')!.onClick());
 await waitFor(()=>expect(action?.disabled).toBe(false));
 expect(api.getPublicationForArtifact).toHaveBeenLastCalledWith('published',expect.anything());
 rendered.rerender(view({...slot,artifact_id:'delayed',revision:2,artifact_value:{text:'# Delayed'}}));
 expect(screen.getByRole('article')).toHaveTextContent('# Published');
 await act(async()=>action!.menu!.find(item=>item.key==='obsidian')!.onClick());
 expect(api.publishDocument).toHaveBeenLastCalledWith('published',expect.objectContaining({base_revision:3,base_draft_version:1}),expect.anything());
});
