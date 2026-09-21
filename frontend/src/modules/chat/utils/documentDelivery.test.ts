import { beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkflowSessionApi } from './request';

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
vi.mock('@/components/request', () => ({ axiosInstance: { defaults: {}, ...http }, BASE_URL: '' }));

// Reflection makes absence of the new adapter a readable missing-capability
// failure without adding a fake implementation to the production module.
function adapter(name: string): (...args: unknown[]) => Promise<unknown> {
  const method = Reflect.get(WorkflowSessionApi(), name);
  expect(method, `${name} must be provided by the real WorkflowSessionApi`).toBeTypeOf('function');
  return method;
}

beforeEach(() => vi.clearAllMocks());
describe('generic document delivery HTTP contract', () => {
  it('loads the server Provider catalog without a frontend whitelist', async () => {
    http.get.mockResolvedValue({ data: { data: { providers: [{ id: 'future-provider', capabilities: ['create', 'replace'] }] } } });
    await adapter('listDocumentProviders')();
    expect(http.get).toHaveBeenCalledWith('/api/core/document-providers', undefined);
  });

  it('publishes by artifact identity with both versions and a stable request key', async () => {
    const input = { provider: 'future-provider', mode: 'replace', idempotency_key: 'request-123' };
    const body = { action: 'publish_document', base_revision: 3, base_draft_version: 7, input };
    http.post.mockResolvedValue({ data: { data: { provider_synced: true, artifact_saved: true } } });
    await adapter('publishDocument')('artifact/with slash', body);
    expect(http.post).toHaveBeenCalledWith(
      '/api/core/workflow-artifacts/artifact%2Fwith%20slash/document-actions:execute', body, undefined,
    );
    expect(http.post).toHaveBeenCalledTimes(1);
  });

  it.each([409, 502, 503])('does not replay a publication after HTTP %s', async (status) => {
    const error = { response: { status, data: { data: { retryable: false, artifact_saved: false } } } };
    http.post.mockRejectedValue(error);
    const body = { action: 'publish_document', base_revision: 3, base_draft_version: 7,
      input: { provider: 'future-provider', mode: 'replace', idempotency_key: 'keep-this-key' } };
    await expect(adapter('publishDocument')('artifact', body)).rejects.toBe(error);
    expect(http.post).toHaveBeenCalledTimes(1);
    expect(http.post.mock.calls[0][1]).toEqual(body);
  });

  it('sends draft version when saving through the Artifact facade', async () => {
    const body = { base_revision: 3, base_draft_version: 7, content_type: 'text/markdown',
      value: { text: '# Edited' }, command_id: 'edit-123' };
    await adapter('saveDocumentArtifact')('artifact', body);
    expect(http.patch).toHaveBeenCalledWith('/api/core/workflow-artifacts/artifact', body,
      { headers: { 'Workflow-Contract-Version': 'workflow.v1' } });
  });
});
