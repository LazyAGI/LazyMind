import { axiosInstance, BASE_URL } from '@/components/request';

const CORE = `${BASE_URL}/api/core`;

export interface StepInfo {
  step_id: string;
  status: string;
  artifact?: string;
}

export interface Task {
  id: string;
  user_id: string;
  conversation_id: string;
  conversation_title?: string;
  plugin_session_id?: string;
  task_type: string;
  title?: string;
  status: string;
  schedule_id?: string;
  schedule_name?: string;
  steps: StepInfo[];
  progress?: unknown;
  created_at: string;
  updated_at: string;
  finished_at?: string;
}

export interface Schedule {
  id: string;
  user_id: string;
  name: string;
  remark: string;
  cron_expr: string;
  timezone: string;
  prompt_template: string;
  kb_ids?: string[];
  file_ids?: string[];
  enabled: boolean;
  run_count: number;
  last_run_at?: string;
  next_run_at: string;
  created_at: string;
}

export interface TaskListResponse {
  items: Task[];
  total: number;
  page: number;
  page_size: number;
}

export interface ScheduleListResponse {
  items: Schedule[];
  total: number;
}

export interface CreateScheduleRequest {
  cron_expr: string;
  prompt_template: string;
  timezone: string;
  name?: string;
  remark?: string;
  kb_ids?: string[];
  file_ids?: string[];
}

export async function listTasks(params: {
  status?: string;
  task_type?: string;
  keyword?: string;
  page?: number;
  page_size?: number;
}): Promise<TaskListResponse> {
  const query = new URLSearchParams();
  if (params.status) query.set('status', params.status);
  if (params.task_type) query.set('task_type', params.task_type);
  if (params.keyword) query.set('keyword', params.keyword);
  if (params.page) query.set('page', String(params.page));
  if (params.page_size) query.set('page_size', String(params.page_size));
  const resp = await axiosInstance.get<TaskListResponse>(
    `${CORE}/task-center/tasks?${query.toString()}`,
  );
  return resp.data;
}

export async function cancelTask(id: string): Promise<void> {
  await axiosInstance.post(`${CORE}/task-center/tasks/${id}:cancel`);
}

export async function removeTask(id: string): Promise<void> {
  await axiosInstance.post(`${CORE}/task-center/tasks/${id}:remove`);
}

export async function addTask(conversationId: string, title?: string): Promise<Task> {
  const resp = await axiosInstance.post<Task>(`${CORE}/task-center/tasks`, {
    conversation_id: conversationId,
    title: title ?? '',
  });
  return resp.data;
}

export async function listSchedules(includeDisabled = false): Promise<ScheduleListResponse> {
  const query = includeDisabled ? '?include_disabled=true' : '';
  const resp = await axiosInstance.get<ScheduleListResponse>(`${CORE}/schedules${query}`);
  return resp.data;
}

export async function createSchedule(req: CreateScheduleRequest): Promise<Schedule> {
  const resp = await axiosInstance.post<Schedule>(`${CORE}/schedules`, req);
  return resp.data;
}

export async function cancelSchedule(id: string): Promise<void> {
  await axiosInstance.post(`${CORE}/schedules/${id}:cancel`);
}

export async function enableSchedule(id: string): Promise<Schedule> {
  const resp = await axiosInstance.post<Schedule>(`${CORE}/schedules/${id}:enable`);
  return resp.data;
}

export async function runScheduleNow(id: string): Promise<{ task_id: string; conversation_id: string }> {
  const resp = await axiosInstance.post(`${CORE}/schedules/${id}:run-now`);
  return resp.data;
}

export async function updateSchedule(id: string, req: Partial<CreateScheduleRequest>): Promise<Schedule> {
  const resp = await axiosInstance.put<Schedule>(`${CORE}/schedules/${id}`, req);
  return resp.data;
}

export async function listScheduleTasks(
  scheduleId: string,
  page: number,
  pageSize = 10,
): Promise<TaskListResponse> {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  const resp = await axiosInstance.get<TaskListResponse>(
    `${CORE}/task-center/schedules/${scheduleId}/tasks?${query.toString()}`,
  );
  return resp.data;
}
