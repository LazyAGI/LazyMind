import { beforeEach, expect, it, vi } from 'vitest';
import { filePreviewTarget, previewKind, readPreviewFile, safePreviewUrl } from './target';
import { toArtifactFiles } from '../ArtifactCollectorCard/artifactFiles';
import type { ConversationArtifact } from '../../store/taskCenter';

const api = vi.hoisted(() => ({ list: vi.fn(), revision: vi.fn() }));
vi.mock('../../utils/request', () => ({
  ArtifactV2Api: () => ({ downloadRevisionUrl: api.revision }),
  TaskServiceApi: () => ({ listConversationArtifacts: api.list }),
}));
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getAuthHeaders: () => ({ Authorization: 'TEST_ONLY_TOKEN' }) } }));
const base: ConversationArtifact = {
  artifact_id: 'artifact', conversation_id: 'conversation', history_id: 'turn', producer_type: 'main_agent',
  content_type: 'file', slot: 'report', seq: 1, revision: 1, filename: 'report.pdf', value: { url: '/static-files/report.pdf' },
};
beforeEach(() => { vi.restoreAllMocks(); api.list.mockReset(); api.revision.mockReset(); });

it('distinguishes versions, same-name files and entries in a file list', () => {
  const targets = [base, { ...base, revision: 2 }, { ...base, artifact_id: 'other' },
    { ...base, artifact_id: 'list', content_type: 'file_list', value: { paths: ['/static-files/a.pdf', '/static-files/b.pdf'] } }]
    .flatMap(artifact => toArtifactFiles([artifact]).map(filePreviewTarget));
  expect(new Set(targets.map(target => target.key)).size).toBe(5);
});

it('refreshes the selected version, never replacing it with a new head', async () => {
  const target = filePreviewTarget(toArtifactFiles([base])[0]);
  api.list.mockResolvedValue({ data: { artifacts: [{ ...base, revision: 2 }] } });
  await expect(target.refreshUrl!()).rejects.toThrow('unavailable');
  api.list.mockResolvedValue({ data: { deliveries: [base], artifacts: [{ ...base, revision: 2 }] } });
  expect(await target.refreshUrl!()).toContain('/static-files/report.pdf');
});

it('uses the exact v2 revision for renewed download URLs', async () => {
  const target = filePreviewTarget(toArtifactFiles([{ ...base, v2_artifact_id: 'v2', revision_id: 'r-old' }])[0]);
  api.revision.mockResolvedValue({ data: { url: '/static-files/old.pdf' } });
  expect(await target.refreshUrl!()).toContain('/static-files/old.pdf');
  expect(api.revision).toHaveBeenCalledWith('r-old');
  expect(api.list).not.toHaveBeenCalled();
});

it('keeps empty inline files valid without a network request', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch');
  const target = filePreviewTarget(toArtifactFiles([{ ...base, filename: 'empty.md', content_type: 'text', value: { text: '' } }])[0]);
  expect((await readPreviewFile(target, new AbortController().signal)).byteLength).toBe(0);
  expect(fetch).not.toHaveBeenCalled();
});

it('never sends application credentials to an external file host', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('file'));
  await readPreviewFile({ key: 'a', filename: 'a.txt', url: 'https://files.example.test/a.txt' }, new AbortController().signal);
  expect(fetch).toHaveBeenCalledWith('https://files.example.test/a.txt', expect.objectContaining({ headers: undefined, credentials: 'omit' }));
});

it('renews an expired address once and rejects continuing access errors', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response('', { status: 403 }));
  const refreshUrl = vi.fn(async () => '/api/core/static-files/new.pdf');
  await expect(readPreviewFile({ key: 'a', filename: 'a.pdf', url: '/api/core/static-files/old.pdf', refreshUrl }, new AbortController().signal)).rejects.toThrow('unavailable');
  expect(refreshUrl).toHaveBeenCalledTimes(1); expect(fetch).toHaveBeenCalledTimes(2);
});

it.each(['javascript:alert(1)', 'file:///etc/passwd', 'https://user:password@example.test/file', 'data:text/html,<script>'])('rejects unsafe file addresses: %s', raw => {
  expect(safePreviewUrl(raw)).toBe('');
});

it.each(['png', 'pdf', 'docx', 'pptx', 'xlsx', 'html', 'md', 'txt', 'json', 'mp3', 'mp4'])('accepts existing %s preview formats', ext => {
  expect(previewKind({ key: ext, filename: `file.${ext}` })).not.toBe('unsupported');
});
