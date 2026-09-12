import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import SkillPackageEditor from './SkillPackageEditor';
import * as api from '../../skillApi';

vi.mock('../../skillApi', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../skillApi')>();
  return { ...original, getSkillTree: vi.fn(), getSkillDraftStatus: vi.fn(),
    getSkillDistributionUpgradeStatus: vi.fn(), compareSkillTreeDiff: vi.fn(),
    probeSkillAgentReviewMode: vi.fn(), compareSkillFileDiff: vi.fn(), readSkillFsFile: vi.fn() };
});
vi.mock('@/modules/knowledge/components/MarkdownViewer', () => ({ default: ({children}: {children: string}) => <article>{children}</article> }));
vi.mock('./SkillDiffHunkPanel', () => ({ default: () => <div>待确认差异</div> }));

const t = (key: string) => ({
  'admin.memorySkillPackagePreview': '预览内容',
  'admin.memorySkillPackageShowDiff': '查看变更',
}[key] || key);
const file = (name: string): api.SkillTreeNodeRecord => ({name, path: name, type:'file', fileType:'markdown', mime:'text/markdown', size:100, binary:false, blobHash:'test', children:[]});

beforeEach(() => {
  vi.mocked(api.getSkillTree).mockResolvedValue({ ...file(''), type:'dir', children:[file('SKILL.md'), file('README.md')] });
  vi.mocked(api.getSkillDraftStatus).mockResolvedValue({baseRevisionId:'base', conversationId:'conv', draftVersion:1, hasUncommittedDraft:true, overlayCount:1, taskId:'task'});
  vi.mocked(api.getSkillDistributionUpgradeStatus).mockResolvedValue({managed:false, updateAvailable:false, pending:false, currentVersion:'', currentArchiveSha256:'', pendingVersion:'', pendingArchiveSha256:'', latestVersion:'', latestArchiveSha256:'', conflicts:[]});
  vi.mocked(api.compareSkillTreeDiff).mockResolvedValue({cacheWritten:false, files:[{path:'SKILL.md', status:'modified', binary:false, type:'file', tooLarge:false, diffEntryLines:[]}]});
  vi.mocked(api.probeSkillAgentReviewMode).mockResolvedValue(true);
  vi.mocked(api.compareSkillFileDiff).mockResolvedValue({path:'SKILL.md', status:'modified', binary:false, type:'file', tooLarge:false, diffEntryLines:[{type:'add', text:'new paragraph'}]});
  vi.mocked(api.readSkillFsFile).mockImplementation(async (_id, path) => ({path, content:path === 'SKILL.md' ? '# 完整技能正文\n\n原有步骤和待确认步骤' : '# 使用说明\n\n未变更的参考内容', binary:false} as Awaited<ReturnType<typeof api.readSkillFsFile>>));
});

describe('pending skill content preview', () => {
  it('offers a read-only full preview while retaining change review', async () => {
    render(<SkillPackageEditor skillId='skill-1' canEdit autoUpdateEnabled={false} t={t} />);
    await waitFor(() => expect(api.readSkillFsFile).toHaveBeenCalledWith('skill-1', 'SKILL.md'));
    fireEvent.click(await screen.findByRole('button', {name:'预览内容'}));
    expect(await screen.findByText(/原有步骤和待确认步骤/)).toBeVisible();
    expect(screen.getByRole('article').querySelector('textarea, [contenteditable=true]')).toBeNull();
    fireEvent.click(screen.getByRole('button', {name:'查看变更'}));
    expect(screen.queryByText(/原有步骤和待确认步骤/)).not.toBeInTheDocument();
  });

  it('shows unchanged reference files as content during pending review', async () => {
    render(<SkillPackageEditor skillId='skill-1' canEdit autoUpdateEnabled={false} t={t} />);
    fireEvent.click(await screen.findByText('README.md'));
    expect(await screen.findByText(/未变更的参考内容/)).toBeVisible();
  });
});
