import { beforeEach, describe, expect, it, vi } from 'vitest';
const http = vi.hoisted(() => ({ post: vi.fn(), put: vi.fn() }));
vi.mock('@/components/request', () => ({ axiosInstance: http }));
import { getSkillPackageValidationError, uploadSkillTempFile } from './skillUpload';

beforeEach(() => {
  vi.clearAllMocks();
  http.post.mockImplementation(async (url: string) => url.endsWith(':initUpload')
    ? { data: { upload_id: 'upload', part_size: 2, total_parts: 2 } }
    : { data: { stored_path: 'skill.zip' } });
  http.put.mockResolvedValue({ data: {} });
});

describe('local skill package validation', () => {
  it('rejects non-ZIP files and oversized ZIP files before any network request', async () => {
    const wrongType = new File(['archive'], 'skill.tgz');
    const tooLarge = new File([], 'skill.zip');
    Object.defineProperty(tooLarge, 'size', { value: 104857601 });
    expect(getSkillPackageValidationError(wrongType)).toBe('type');
    expect(getSkillPackageValidationError(tooLarge)).toBe('size');
    await expect(uploadSkillTempFile(wrongType, { validatePackage: true })).rejects.toBeDefined();
    await expect(uploadSkillTempFile(tooLarge, { validatePackage: true })).rejects.toBeDefined();
    expect(http.post).not.toHaveBeenCalled();
  });
});

describe('multipart skill upload', () => {
  it('reports byte progress across parts and forwards cancellation to every request', async () => {
    const signal = new AbortController().signal;
    const progress: number[] = [];
    http.put.mockImplementation(async (_url, _chunk, options) => {
      options.onUploadProgress({ loaded: 1 });
      return { data: {} };
    });
    const result = await uploadSkillTempFile(new File(['abcd'], 'script.py'), { signal, onProgress: percent => progress.push(percent) });
    expect(result.uploadId).toBe('upload');
    expect(progress).toEqual([0, 25, 50, 75, 100]);
    expect(http.post.mock.calls.every(call => call[2].signal === signal)).toBe(true);
    expect(http.put.mock.calls.every(call => call[2].signal === signal)).toBe(true);
  });

  it('does not send later parts or complete a canceled upload', async () => {
    const controller = new AbortController();
    http.put.mockImplementationOnce(async () => { controller.abort(); return { data: {} }; });
    await expect(uploadSkillTempFile(new File(['abcd'], 'skill.zip'), { signal: controller.signal })).rejects.toBeDefined();
    expect(http.put).toHaveBeenCalledTimes(1);
    expect(http.post).toHaveBeenCalledTimes(2);
    expect(http.post.mock.calls[1][0]).toContain('temp/uploads/upload:abort');
  });
});
