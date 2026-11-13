import { beforeEach, expect, it, vi } from 'vitest';

const http = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http }));
import { readLocalGitHubStarCapability } from './api';

beforeEach(() => { vi.clearAllMocks(); });

it.each([true, false])('reads local capability without a Star or support write: %s', async eligible => {
  const capability = { repository: 'LazyAGI/LazyMind', eligible };
  http.get.mockResolvedValue({ data: { data: capability } });
  await expect(readLocalGitHubStarCapability()).resolves.toEqual(capability);
  expect(http.get).toHaveBeenCalledWith('/api/authservice/v1/cloud/github/star/local', expect.any(Object));
  expect(http.get).toHaveBeenCalledTimes(1);
  expect(http.put).not.toHaveBeenCalled();
  expect(http.post).not.toHaveBeenCalled();
});
