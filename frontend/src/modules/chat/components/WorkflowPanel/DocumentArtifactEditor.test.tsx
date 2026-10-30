const availability = vi.hoisted(() => ({ states: { feishu: 'ready', github: 'ready', wechat: 'ready', obsidian: 'ready', notion: 'ready' },
  accounts: Object.fromEntries(['feishu', 'github', 'wechat', 'notion'].map(provider => [provider, [{ connection_id: `fixture-${provider}` }]])),
  refresh: vi.fn() }));
vi.mock('./useWriterProviderAvailability', () => ({ useWriterProviderAvailability: () => availability }));
import { act, fireEvent, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ConfigProvider, Dropdown } from 'antd';
import i18n from '@/i18n';
import type { DocumentPublicationGroupState } from '@/api/generated/core-client';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import type { WriterNumberingState } from '@/modules/chat/utils/request';
import { DocumentArtifactEditor } from './DocumentArtifactEditor';
import { SlotEditingContext, type SlotFooterAction } from './slotEditingContext';
import { documentImageGroups } from './writerImageGroups';

const api = vi.hoisted(() => ({ getDocumentArtifact: vi.fn(), convertDocument: vi.fn(), saveDocumentArtifact: vi.fn(), previewDocumentAction: vi.fn(), listDocumentProviders: vi.fn(), publishDocument: vi.fn(), getPublicationTargets: vi.fn(async () => ({ data: { data: { current_hash: 'fixture', preferred_providers: [], targets: [] } as DocumentPublicationGroupState } })), createPublicationGroup: vi.fn(), getPublicationForArtifact: vi.fn(async()=>({data:{data:{}}})) }));
const download = vi.hoisted(() => vi.fn());
const confirm = vi.hoisted(() => vi.fn());
vi.mock('@/modules/chat/utils/request', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => api,
}));
vi.mock('antd', async (importOriginal) => ({
  ...await importOriginal<typeof import('antd')>(), Modal: Object.assign((await importOriginal<typeof import('antd')>()).Modal, { confirm }),
}));

vi.mock('./MarkdownArtifactEditor', () => ({
  MarkdownArtifactEditor: ({ markdown, sourceRevision, onSave, numbering }: { markdown: string; sourceRevision: number; onSave: (text: string, revision: number, mode: 'draft') => Promise<unknown>; numbering?: WriterNumberingState }) => <>
    <article>{markdown}</article>
    <output data-testid='heading-number'>{numbering?.entries['sec-1']?.label}</output>
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

afterEach(() => { cleanup(); vi.clearAllMocks(); api.getPublicationTargets.mockReset().mockResolvedValue({ data: { data: { current_hash: 'fixture', preferred_providers: [], targets: [] } } }); availability.refresh.mockReset(); availability.states = { feishu: 'ready', github: 'ready', wechat: 'ready', obsidian: 'ready', notion: 'ready' }; });

it.each([false, true])('preserves the WeChat image-group choice before multi-platform publication (continue: %s)', async proceed => {
  await i18n.changeLanguage('en-US');
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'wechat' }, { id: 'notion' }] } } });
  api.createPublicationGroup.mockResolvedValue({ data: { data: { current_hash: 'saved', preferred_providers: [], targets: [] } } });
  api.saveDocumentArtifact.mockImplementation(async (id, body) => ({ data: { ok: true, result: {
    artifact_id: id, revision: 1, draft_version: 2, value: body.value,
  } } }));
  const markdown = '# Photos\n\n![First](https://example.org/one.png)\n\n![Second](https://example.org/two.png)\n\n![Third](https://example.org/three.png)';
  const slot = { artifact_id: 'images', slot_id: 'draft_document', revision: 1, draft_version: 1,
    artifact_value: { text: markdown }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  let action: SlotFooterAction | undefined;
  render(<ConfigProvider theme={{ token: { motion: false } }}><SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider></ConfigProvider>);
  await waitFor(() => expect(action?.disabled).toBe(false));
  act(() => action!.onClick());
  fireEvent.click(await screen.findByRole('checkbox', { name: String(i18n.t('chat.writerIR.providers.wechat')) }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'Notion' }));
  fireEvent.click(screen.getByRole('button', { name: /Publish to / }));
  await waitFor(() => expect(confirm).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  expect(api.saveDocumentArtifact).not.toHaveBeenCalled();
  await act(async () => confirm.mock.calls[0][0][proceed ? 'onOk' : 'onCancel']());
  if (proceed) {
    await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
    expect(documentImageGroups(api.saveDocumentArtifact.mock.calls[0][1].value.text)).toEqual([
      expect.objectContaining({ layout: 'vertical' }),
    ]);
    expect(api.createPublicationGroup).toHaveBeenCalledWith('images', expect.objectContaining({ base_draft_version: 2, targets: [
      expect.objectContaining({ provider: 'wechat' }), expect.objectContaining({ provider: 'notion' }),
    ] }), expect.anything());
  } else {
    expect(api.createPublicationGroup).not.toHaveBeenCalled();
    expect(api.saveDocumentArtifact).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByRole('article').textContent).toBe(markdown);
  }
});

it('both publish entrances open confirmation without saving or publishing on cancel', async () => {
  await i18n.changeLanguage('en-US');
  api.listDocumentProviders.mockResolvedValue({ data:{ data:{ providers:[{id:'github'},{id:'notion'}] } } });
  let action: SlotFooterAction | undefined;
  const slot={artifact_id:'confirmation',slot_id:'draft_document',revision:1,artifact_value:{text:'# Retained draft'},document:{representation:'markdown',editable:true,capabilities:['save','publish_document']}} as SlotRevision;
  render(<SlotEditingContext.Provider value={{setEditing:vi.fn(),registerFlush:()=>()=>{},registerFooterAction:(_key,next)=>{if(next?.icon==='write-back')action=next;return ()=>{};}}}>
    <DocumentArtifactEditor slot={slot} sessionId='fixture'/>
  </SlotEditingContext.Provider>);
  await waitFor(()=>expect(action?.disabled).toBe(false));
  expect(action?.flushBeforeAction).toBe(false);
  act(()=>action!.onClick());
  expect(await screen.findByRole('checkbox',{name:'GitHub'})).not.toBeChecked();
  fireEvent.click(screen.getByRole('button',{name:'Cancel'}));
  act(()=>action!.menu!.find(item=>item.key==='notion')!.onClick());
  await waitFor(()=>expect(screen.getByRole('checkbox',{name:'Notion'})).toBeChecked());
  expect(api.saveDocumentArtifact).not.toHaveBeenCalled();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  expect(screen.getByRole('article')).toHaveTextContent('# Retained draft');
});

it.each([false, true])('uses the same Obsidian binding state in the menu and dialog (bound: %s)', async bound => {
  await i18n.changeLanguage('en-US');
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'notion' }, { id: 'obsidian' }] } } });
  let finish!: (value: Awaited<ReturnType<typeof api.getPublicationTargets>>) => void;
  api.getPublicationTargets.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  api.getPublicationTargets.mockResolvedValue({ data: { data: { current_hash: 'fixture', preferred_providers: [], targets: [{ provider: 'obsidian', bound }] } } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'obsidian-menu', slot_id: 'draft_document', revision: 1, artifact_value: { text: '# Draft' },
    document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.menu?.find(item => item.key === 'obsidian')).toBeDefined());
  const initial = action!.menu!.find(item => item.key === 'obsidian')!;
  const menu = render(<Dropdown open menu={{ items: action!.menu }}><button>publish menu</button></Dropdown>);
  expect(screen.getByRole('menuitem', { name: /Obsidian/ })).toHaveAttribute('aria-disabled', 'true');
  act(() => initial.onClick());
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  await act(async () => finish({ data: { data: { current_hash: 'fixture', preferred_providers: [], targets: [{ provider: 'obsidian', bound }] } } }));
  await waitFor(() => expect(action!.menu!.find(item => item.key === 'obsidian')!.disabled).toBe(!bound));
  menu.rerender(<Dropdown open menu={{ items: action!.menu }}><button>publish menu</button></Dropdown>);
  const menuItem = screen.getByRole('menuitem', { name: /Obsidian/ });
  expect(menuItem).toHaveTextContent(bound ? 'Obsidian' : 'Obsidian · Bound notes only');
  expect(menuItem).toHaveTextContent('Desktop / self-hosted');
  const guide = screen.getByRole('link', { name: 'View Obsidian setup guide' });
  expect(guide).toHaveAttribute('href', '/cloud-documents/docs/obsidian-setup');
  expect(guide).toHaveAttribute('target', '_blank');
  expect(guide).toHaveAttribute('rel', 'noopener noreferrer');
  guide.focus();
  expect(guide).toHaveFocus();
  fireEvent.keyDown(guide, { key: 'Enter' });
  fireEvent.click(guide);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  if (bound) expect(menuItem).not.toHaveAttribute('aria-disabled', 'true');
  else expect(menuItem).toHaveAttribute('aria-disabled', 'true');
  const item = action!.menu!.find(item => item.key === 'obsidian')!;
  act(() => item.onClick());
  if (!bound) {
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    act(() => action!.onClick());
  }
  const checkbox = await screen.findByRole('checkbox', { name: 'Obsidian' });
  if (bound) {
    expect(checkbox).toBeEnabled();
    expect(checkbox).toBeChecked();
  } else {
    expect(checkbox).toBeDisabled();
    expect(checkbox).not.toBeChecked();
  }
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it('opens each unavailable provider settings without selecting a publish action or losing the edited draft', async () => {
  await i18n.changeLanguage('zh-CN');
  const targets = [
    ['feishu', '/cloud-documents/feishu'], ['github', '/cloud-documents'],
    ['googledrive', '/cloud-documents/google-drive'], ['notion', '/cloud-documents'],
    ['wechat', '/cloud-documents/wechat-official-account'],
  ];
  Object.assign(availability.states, Object.fromEntries(targets.map(([id]) => [id, 'authorize'])));
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [...targets.map(([id]) => ({ id })), { id: 'obsidian' }] } } });
  api.saveDocumentArtifact.mockResolvedValue({ data: { ok: true, result: {
    artifact_id: 'menu-draft', revision: 3, draft_version: 8, value: { text: '# Updated draft' },
  } } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'menu-draft', slot_id: 'draft_document', revision: 3, draft_version: 7,
    artifact_value: { text: '# Draft' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.disabled).toBe(false));
  fireEvent.click(screen.getByRole('button', { name: 'save draft fixture' }));
  await waitFor(() => expect(screen.getByRole('article')).toHaveTextContent('# Updated draft'));
  const selectProvider = vi.fn();
  await act(async () => { render(<Dropdown open menu={{ items: action!.menu!.map(option => ({ ...option,
    onClick: () => { selectProvider(); option.onClick(); },
  })) }}><button>publish menu</button></Dropdown>); });
  const links = screen.getAllByRole('link', { name: /去授权/ });
  expect(links).toHaveLength(targets.length);
  for (const [index, [, path]] of targets.entries()) {
    expect(links[index]).toHaveAttribute('href', `${window.location.origin}${path}`);
    expect(links[index]).toHaveAttribute('target', '_blank');
    expect(links[index]).toHaveAttribute('rel', 'noopener noreferrer');
    fireEvent.keyDown(links[index], { key: 'Enter', keyCode: 13 });
    fireEvent.click(links[index]);
  }
  expect(selectProvider).not.toHaveBeenCalled();
  expect(api.publishDocument).not.toHaveBeenCalled();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(screen.getByRole('article')).toHaveTextContent('# Updated draft');
});

it.each(['ready', 'checking', 'failed', 'chat-disabled'])('only offers a settings shortcut for actionable availability: %s', async state => {
  await i18n.changeLanguage('zh-CN');
  availability.states.feishu = state;
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'feishu' }, { id: 'obsidian' }] } } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'menu-state', slot_id: 'draft_document', revision: 1,
    artifact_value: { text: '# Draft' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='fixture' /></SlotEditingContext.Provider>);
  await waitFor(() => expect(action?.disabled).toBe(false));
  await act(async () => { render(<Dropdown open menu={{ items: action!.menu }}><button>publish menu</button></Dropdown>); });
  if (state === 'chat-disabled') {
    expect(screen.getByRole('link', { name: /去设置/ })).toHaveAttribute('href', `${window.location.origin}/cloud-documents/feishu`);
  } else {
    expect(screen.queryByRole('link', { name: /去授权|去设置/ })).not.toBeInTheDocument();
  }
});

it('uses the saved draft for copying and keeps heading numbers visible across a parent refresh', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
  api.convertDocument.mockResolvedValue({ data: { code: 0, data: { format: 'latex', content: 'converted fixture' } } });
  const numbered = (label: string) => ({ data: { data: { representation: 'markdown', document: '# Draft',
    numbering: { ordered_style: 'hierarchical', entries: { 'sec-1': { label, mode: 'ordered', restart: false } } } } } });
  let finishNumbering!: (response: ReturnType<typeof numbered>) => void;
  api.previewDocumentAction.mockReset().mockResolvedValueOnce(numbered('1.'))
    .mockImplementationOnce(() => new Promise(resolve => { finishNumbering = resolve; }));
  api.saveDocumentArtifact.mockResolvedValue({ data: { ok: true, result: {
    artifact_id: 'copy-fixture', revision: 3, draft_version: 8, value: { text: '# Updated draft' },
  } } });
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'copy-fixture', slot_id: 'draft_document', revision: 3, draft_version: 7,
    artifact_value: { text: '# Draft' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'convert_document', 'numbering'] } } as SlotRevision;
  const editor = (slot: SlotRevision) => <SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'copy') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='session' /></SlotEditingContext.Provider>;
  const view = render(editor(slot));
  await waitFor(() => expect(action).toBeDefined());
  await waitFor(() => expect(screen.getByTestId('heading-number')).toHaveTextContent('1.'));
  act(() => action!.menu!.find(item => item.key === 'latex')!.onClick());
  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
  expect(api.convertDocument).toHaveBeenLastCalledWith('session', 'draft_document', -1, 3, 'latex', '# Draft', 7);
  fireEvent.click(screen.getByRole('button', { name: 'save draft fixture' }));
  await waitFor(() => expect(screen.getByRole('article')).toHaveTextContent('# Updated draft'));
  expect(screen.getByTestId('heading-number')).toHaveTextContent('1.');
  act(() => action!.onClick());
  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(2));
  expect(api.convertDocument).toHaveBeenLastCalledWith('session', 'draft_document', -1, 3, 'latex', '# Updated draft', 8);
  view.rerender(editor({ ...slot, draft_version: 8, artifact_value: { text: '# Updated draft' },
    document: { ...slot.document!, capabilities: [...slot.document!.capabilities] } }));
  expect(api.previewDocumentAction).toHaveBeenCalledTimes(2);
  expect(screen.getByTestId('heading-number')).toHaveTextContent('1.');
  await act(async () => { finishNumbering(numbered('2.')); });
  expect(screen.getByTestId('heading-number')).toHaveTextContent('2.');
});

describe('document artifact display', () => {


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
