import { axiosInstance, BASE_URL } from '@/components/request';

const CORE = `${BASE_URL}/api/core`;

export interface Task {
  id: string;
  user_id: string;
  conversation_id: string;
  plugin_session_id?: string;
  task_type: string;
  title?: string;
  status: string;
  schedule_id?: string;
  progress?: unknown;
  created_at: string;
  updated_at: string;
  finished_at?: string;
}

export interface Schedule {
  id: string;
  user_id: string;
  conversation_id?: string;
  cron_expr: string;
  timezone: string;
  prompt_template: string;
  enabled: boolean;
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
  conversation_id?: string;
}

export async function listTasks(params: {
  status?: string;
  task_type?: string;
  page?: number;
  page_size?: number;
}): Promise<TaskListResponse> {
  const query = new URLSearchParams();
  if (params.status) query.set('status', params.status);
  if (params.task_type) query.set('task_type', params.task_type);
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

export async function listSchedules(): Promise<ScheduleListResponse> {
  const resp = await axiosInstance.get<ScheduleListResponse>(`${CORE}/schedules`);
  return resp.data;
}

export async function createSchedule(req: CreateScheduleRequest): Promise<Schedule> {
  const resp = await axiosInstance.post<Schedule>(`${CORE}/schedules`, req);
  return resp.data;
}

export async function cancelSchedule(id: string): Promise<void> {
  await axiosInstance.post(`${CORE}/schedules/${id}:cancel`);
}
