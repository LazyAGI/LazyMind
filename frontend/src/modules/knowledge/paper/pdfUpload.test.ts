import { beforeEach, describe, expect, it, vi } from 'vitest';

const requests = vi.hoisted(() => ({ build: vi.fn(), create: vi.fn(), start: vi.fn(), get: vi.fn(), resume: vi.fn() }));
vi.mock('../utils/uploadByHash', () => ({ buildUploadTaskItems: requests.build }));
vi.mock('@/api/generated/core-client', () => ({
  TasksApiFactory: () => ({
    apiCoreDatasetsDatasetTasksPost: requests.create,
    apiCoreDatasetsDatasetTasksStartPost: requests.start,
    apiCoreDatasetsDatasetTasksTaskGet: requests.get,
    apiCoreDatasetsDatasetTasksTaskResumePost: requests.resume,
  }),
  DefaultApiFactory: () => ({}),
}));
vi.mock('@/components/request', () => ({
  BASE_URL: '', axiosInstance: { defaults: {}, interceptors: { request: { use: vi.fn() } } },
  localizeErrorCode: (_code: string, fallback = '') => fallback,
}));

import { createPaperPdfUploader } from './pdfUpload';
import {paperFixture} from './__tests__/fixtures';

const file = new File(['%PDF-study'], 'study.pdf', { type: 'application/pdf' });
const task = { task_id: 'task-1', document_id: 'document-1', display_name: file.name, task_state: 'WAITING' };
const started = { data: { requested_count: 1, started_count: 1, failed_count: 0, tasks: [{ ...task, status: 'STARTED', submit_status: 'SUBMITTED' }] } };
const rejected = { data: { requested_count: 1, started_count: 0, failed_count: 1, tasks: [{ ...task, status: 'FAILED', submit_status: 'REJECTED', message: 'parser unavailable' }] } };

beforeEach(() => {
  Object.values(requests).forEach((request) => request.mockReset());
  requests.build.mockResolvedValue([{ upload_file_id: 'upload-1', task: { display_name: file.name } }]);
  requests.create.mockResolvedValue({ data: { tasks: [task] } });
  requests.start.mockResolvedValue(started);
  requests.resume.mockResolvedValue(started);
  requests.get.mockResolvedValue({ data: { ...task, task_state: 'WAITING' } });
});

describe('confirmed PDF upload retries', () => {
  it('attaches a durable operation context to the existing upload request', async () => {
    const context = { idempotency_key: 'operation-key', library_paper_id: 'paper', title: 'Study',paper:paperFixture({metadata:{title:'Study'}}) };
    await createPaperPdfUploader('papers')(file, 'DEFAULT', context);
    expect(requests.create.mock.calls[0][0].createTaskRequest.paper_import).toEqual(context);
  });

  it('reuses the created document and task after a rejected start', async () => {
    const upload = createPaperPdfUploader('papers');
    requests.start.mockResolvedValueOnce(rejected);
    await expect(upload(file, 'DEFAULT')).rejects.toThrow('parser unavailable');
    await expect(upload(file, 'DEFAULT')).resolves.toMatchObject({ documentId: 'document-1', tasks: [task] });
    expect(requests.build).toHaveBeenCalledOnce();
    expect(requests.create).toHaveBeenCalledOnce();
    expect(requests.get).toHaveBeenCalledWith({ dataset: 'papers', task: 'task-1' }, undefined);
    expect(requests.start).toHaveBeenLastCalledWith({ dataset: 'papers', startTaskRequest: { task_ids: ['task-1'], start_mode: 'DEFAULT' } }, undefined);
  });

  it.each(['WORKING', 'SUCCESS'])('recovers %s after a lost start response without submitting again', async (state) => {
    const upload = createPaperPdfUploader('papers');
    requests.start.mockRejectedValueOnce(new Error('connection reset'));
    requests.get.mockResolvedValue({ data: { ...task, task_state: state, err_msg: state === 'SUCCESS' ? 'success' : '' } });
    await expect(upload(file, 'DEFAULT')).rejects.toThrow('connection reset');
    await expect(upload(file, 'DEFAULT')).resolves.toMatchObject({ documentId: 'document-1' });
    expect(requests.create).toHaveBeenCalledOnce();
    expect(requests.start).toHaveBeenCalledOnce();
  });

  it('resumes the same failed task and keeps a second failure visible', async () => {
    const upload = createPaperPdfUploader('papers');
    requests.start.mockResolvedValueOnce(rejected);
    requests.get.mockResolvedValue({ data: { ...task, task_state: 'FAILED', err_msg: 'parser unavailable' } });
    requests.resume.mockResolvedValueOnce(rejected);
    await expect(upload(file, 'DEFAULT')).rejects.toThrow('parser unavailable');
    await expect(upload(file, 'DEFAULT')).rejects.toThrow('parser unavailable');
    expect(requests.create).toHaveBeenCalledOnce();
    expect(requests.resume).toHaveBeenCalledWith({ dataset: 'papers', task: 'task-1' }, undefined);
    await expect(upload(file, 'DEFAULT')).resolves.toMatchObject({ documentId: 'document-1' });
    expect(requests.create).toHaveBeenCalledOnce();
  });

  it.each([
    { ...task, task_state: 'UNKNOWN' },
    { ...task, task_state: 'WORKING', err_msg: 'upstream unavailable' },
    { ...task, task_id: 'other-task', task_state: 'SUCCESS' },
    { ...task, document_id: 'other-document', task_state: 'SUCCESS' },
  ])('does not hide an unverified retry outcome: %j', async (recovered) => {
    const upload = createPaperPdfUploader('papers');
    requests.start.mockRejectedValueOnce(new Error('connection reset'));
    await expect(upload(file, 'DEFAULT')).rejects.toThrow();
    requests.get.mockResolvedValue({ data: recovered });
    await expect(upload(file, 'DEFAULT')).rejects.toThrow();
    expect(requests.create).toHaveBeenCalledOnce();
    expect(requests.start).toHaveBeenCalledOnce();
  });

  it('retains task identity when retry status cannot be read', async () => {
    const upload = createPaperPdfUploader('papers');
    requests.start.mockRejectedValueOnce(new Error('connection reset'));
    await expect(upload(file, 'DEFAULT')).rejects.toThrow();
    requests.get.mockRejectedValueOnce(new Error('status unavailable'));
    await expect(upload(file, 'DEFAULT')).rejects.toThrow('status unavailable');
    requests.get.mockResolvedValue({ data: { ...task, task_state: 'WORKING' } });
    await expect(upload(file, 'DEFAULT')).resolves.toMatchObject({ documentId: 'document-1' });
    expect(requests.create).toHaveBeenCalledOnce();
  });

  it.each([
    { failure: new Error('connection reset') },
    { response: { data: { requested_count: 1, started_count: 0, failed_count: 1, tasks: [] } } },
    { response: { data: { requested_count: 1, started_count: 0, failed_count: 1, tasks: [{ ...task, status: 'UNKNOWN' }] } } },
  ])('does not resubmit a still-creating task after an ambiguous outcome: %j', async ({ failure, response }) => {
    const upload = createPaperPdfUploader('papers');
    if (failure) requests.start.mockRejectedValueOnce(failure);
    else requests.start.mockResolvedValueOnce(response);
    await expect(upload(file, 'DEFAULT')).rejects.toThrow();
    await expect(upload(file, 'DEFAULT')).rejects.toThrow();
    expect(requests.create).toHaveBeenCalledOnce();
    expect(requests.start).toHaveBeenCalledOnce();
    requests.get.mockResolvedValue({ data: { ...task, task_state: 'WORKING' } });
    await expect(upload(file, 'DEFAULT')).resolves.toMatchObject({ documentId: 'document-1' });
    expect(requests.start).toHaveBeenCalledOnce();
  });

  it.each(['WORKING','SUCCESS'])('reuses a hash-matched %s task returned by Core without starting it again',async state=>{
    requests.create.mockResolvedValue({data:{tasks:[{...task,task_state:state}]}});
    await expect(createPaperPdfUploader('papers')(file,'DEFAULT')).resolves.toMatchObject({documentId:'document-1'});
    expect(requests.start).not.toHaveBeenCalled();expect(requests.resume).not.toHaveBeenCalled();
  });

  it('does not reuse task identity in another dataset', async () => {
    const first = createPaperPdfUploader('first');
    const second = createPaperPdfUploader('second');
    await first(file, 'DEFAULT');
    await second(file, 'UPLOAD');
    expect(requests.create).toHaveBeenCalledTimes(2);
    expect(requests.start).toHaveBeenLastCalledWith({ dataset: 'second', startTaskRequest: { task_ids: ['task-1'], start_mode: 'UPLOAD' } }, undefined);
  });
});
