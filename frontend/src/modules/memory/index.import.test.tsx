import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useOutletContext } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useChatInputStore } from '@/modules/chat/store/chatInput';
import { desktopTestTranslation } from '@/test/desktopResourceFixtures';
import MemoryManagement from './index';

const api = vi.hoisted(() => ({ createSkillAsset: vi.fn(), listSkillAssetsPage: vi.fn(), listSkillCategories: vi.fn(), listSkillTags: vi.fn(), getSkillReviewSummary: vi.fn(), uploadSkillTempFile: vi.fn() }));
vi.mock('./skillApi', async importOriginal => ({ ...(await importOriginal<typeof import('./skillApi')>()), ...api }));
vi.mock('./skillUpload', async importOriginal => ({ ...(await importOriginal<typeof import('./skillUpload')>()), uploadSkillTempFile: api.uploadSkillTempFile }));
vi.mock('@/runtime/features', () => ({ runtimeFeatures: { hideUserGroupSurfaces: true } }));
vi.mock('react-i18next', async importOriginal => ({ ...(await importOriginal<typeof import('react-i18next')>()), useTranslation: () => ({ t: desktopTestTranslation }) }));
vi.mock('./components/MemoryDraftModal', () => ({ default: () => null }));
vi.mock('./components/ShareModal', () => ({ default: () => null }));
vi.mock('./components/SkillShareCenterModal', () => ({ default: () => null }));
vi.mock('./components/GlossaryInboxModal', () => ({ default: () => null }));
vi.mock('./pages/list', () => ({ default: () => null }));

function Controls() {
  const context = useOutletContext<any>();
  return <>
    <button onClick={() => context.openSkillCreateModal('url')}>Import URL</button>
    <button onClick={() => context.setCategory('current')}>Change category</button>
    <p aria-label='Loaded rows'>{context.skillAssets.map((item: { name: string }) => item.name).join(',')}</p>
    <p aria-label='Available tags'>{context.availableTags.join(',')}</p>
  </>;
}
function mount() {
  return render(<MemoryRouter initialEntries={['/memory-management/skills']}><Routes>
    <Route path='/memory-management' element={<MemoryManagement />}><Route path='skills' element={<Controls />} /></Route>
    <Route path='/agent/chat/home' element={<div>Chat home</div>} />
  </Routes></MemoryRouter>);
}
beforeEach(() => {
  vi.clearAllMocks();
  useChatInputStore.getState().setModeDraft();
  api.listSkillAssetsPage.mockImplementation(async ({ category }) => ({ records: [{ id: category || 'initial', name: category || 'initial', description: '', category: 'external', tags: [], content: '' }], total: 1, page: 1, pageSize: 20 }));
  api.listSkillCategories.mockResolvedValue(['external']);
  api.listSkillTags.mockResolvedValue(['initial']);
  api.getSkillReviewSummary.mockResolvedValue({ runningTask: null });
  api.createSkillAsset.mockResolvedValue('imported');
});

describe('personal skill import', () => {
  it('shows local upload progress and cancels without creating a skill from a late upload', async () => {
    let finish!: (value: { uploadId: string; storedPath: string }) => void;
    api.uploadSkillTempFile.mockImplementation((_file, options) => {
      options.onProgress(25);
      return new Promise(resolve => { finish = resolve; });
    });
    const { container } = mount();
    fireEvent.change(container.querySelector('input[type=file]')!, { target: { files: [new File(['zip'], 'writer.zip')] } });
    const status = await screen.findByRole('status');
    expect(status).toHaveTextContent('25');
    expect(status).toHaveTextContent('writer.zip');
    const signal = api.uploadSkillTempFile.mock.calls[0][1].signal as AbortSignal;
    fireEvent.click(within(status).getByRole('button', { name: /取\s*消/ }));
    expect(signal.aborted).toBe(true);
    await act(async () => { finish({ uploadId: 'late', storedPath: 'writer.zip' }); });
    expect(api.createSkillAsset).not.toHaveBeenCalled();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('refreshes current filters and tags after a local import completes', async () => {
    let finish!: (value: { uploadId: string; storedPath: string }) => void;
    api.uploadSkillTempFile.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    const { container } = mount();
    await waitFor(() => expect(screen.getByLabelText('Available tags')).toHaveTextContent('initial'));
    fireEvent.change(container.querySelector('input[type=file]')!, { target: { files: [new File(['zip'], 'writer.zip')] } });
    fireEvent.click(screen.getByRole('button', { name: 'Change category' }));
    await waitFor(() => expect(screen.getByLabelText('Loaded rows')).toHaveTextContent('current'));
    api.listSkillTags.mockResolvedValue(['initial', 'imported-tag']);
    await act(async () => { finish({ uploadId: 'uploaded', storedPath: 'writer.zip' }); });
    await waitFor(() => expect(screen.getByLabelText('Available tags')).toHaveTextContent('imported-tag'));
    expect(screen.getByLabelText('Loaded rows')).toHaveTextContent('current');
    expect(api.listSkillAssetsPage.mock.lastCall?.[0].category).toBe('current');
    expect(api.createSkillAsset.mock.calls[0][0]).not.toHaveProperty('tags');
  });

  it('preserves an oversized URL as an editable Chat draft', async () => {
    api.createSkillAsset.mockRejectedValueOnce({ response: { status: 413 } });
    mount();
    fireEvent.click(screen.getByRole('button', { name: 'Import URL' }));
    const url = 'https://github.com/example/skills/tree/main/writer?plain=1#readme';
    fireEvent.change(screen.getByRole('textbox'), { target: { value: ` ${url} ` } });
    fireEvent.click(screen.getByRole('button', { name: /导\s*入/ }));
    expect(api.createSkillAsset.mock.calls[0][0]).not.toHaveProperty('name');
    expect(api.createSkillAsset.mock.calls[0][0]).not.toHaveProperty('description');
    expect(api.createSkillAsset.mock.calls[0][0]).not.toHaveProperty('tags');
    const alert = await screen.findByRole('alert');
    expect(screen.getByRole('textbox')).toHaveValue(` ${url} `);
    fireEvent.click(within(alert).getByRole('button', { name: '去对话中安装' }));
    expect(await screen.findByText('Chat home')).toBeInTheDocument();
    expect(useChatInputStore.getState().modeDraft?.text).toBe(`请帮我从以下链接安装技能：\n${url}`);
  });
});
