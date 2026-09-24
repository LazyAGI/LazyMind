import type { AxiosRequestConfig } from 'axios';
import type { NotificationUpdate } from '@/modules/notifications/api';
import { Configuration, DefaultApi, TaskNotificationsApi } from '@/api/generated/core-client';
import { axiosInstance, BASE_URL } from '@/components/request';

const defaultClient = new DefaultApi(new Configuration({ basePath: BASE_URL }), BASE_URL, axiosInstance);
const notificationClient = new TaskNotificationsApi(new Configuration({ basePath: BASE_URL }), BASE_URL, axiosInstance);

export interface StepInfo {
  step_id: string;
  title?: string;
  status: string;
  current_phase?: string;
  summary?: string;
  artifact?: string;
}

export interface Task {
  id: string;
  user_id: string;
  conversation_id: string;
  conversation_state: 'active' | 'archived' | 'trash' | 'missing';
  conversation_title?: string;
  workflow_session_id?: string;
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
  waiting_reason?: string;
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
  group_id?: string;
  group_position: number;
  dependencies?: ScheduleDependency[];
  enabled: boolean;
  run_count: number;
  last_run_at?: string;
  next_run_at: string;
  created_at: string;
}

export interface ScheduleDependency {
  id?: string;
  source_schedule_id: string;
  source_name?: string;
  window_type?: string;
  content_types?: string[];
  incomplete_policy?: string;
  max_wait_seconds?: number;
}

export interface AutomationGroup {
  id: string;
  name: string;
  remark: string;
  timezone: string;
  enabled: boolean;
  task_count: number;
  created_at: string;
}

export interface TaskListResponse {
  items: Task[];
  total: number;
  page: number;
  page_size: number;
  status_counts?: {
    all: number;
    pending: number;
    waiting: number;
    waiting_inputs: number;
    running: number;
    succeeded: number;
    failed: number;
    canceled: number;
  };
}

export interface ScheduleListResponse {
  items: Schedule[];
  total: number;
}

export interface CreateScheduleRequest {
  notification?: NotificationUpdate;
  cron_expr: string;
  prompt_template: string;
  timezone: string;
  name: string;
  remark?: string;
  kb_ids?: string[];
  file_ids?: string[];
  group_id?: string;
  dependencies?: ScheduleDependency[];
}

export async function listTasks(params: {
  status?: string;
  task_type?: string;
  keyword?: string;
  page?: number;
  page_size?: number;
}): Promise<TaskListResponse> {
  const response = await defaultClient.apiCoreTaskCenterTasksGet({
    status: params.status,
    taskType: params.task_type,
    keyword: params.keyword,
    page: params.page,
    pageSize: params.page_size,
  });
  return response.data as unknown as TaskListResponse;
}

export async function cancelTask(id: string): Promise<void> {
  await defaultClient.apiCoreTaskCenterTasksTaskIdCancelPost({ taskId: id });
}

export async function getTask(id: string): Promise<Task> {
  const response = await defaultClient.apiCoreTaskCenterTasksTaskIdGet(
    { taskId: id },
    { silentError: true } as AxiosRequestConfig & { silentError: boolean },
  );
  return response.data as unknown as Task;
}

export async function removeTask(id: string): Promise<void> {
  await defaultClient.apiCoreTaskCenterTasksTaskIdRemovePost({ taskId: id });
}

export async function listSchedules(includeDisabled = false): Promise<ScheduleListResponse> {
  const response = await defaultClient.apiCoreSchedulesGet({
    includeDisabled: includeDisabled || undefined,
  });
  return response.data;
}

export async function createSchedule(req: CreateScheduleRequest): Promise<Schedule> {
  const response = await notificationClient.apiCoreSchedulesPost({ apiCoreSchedulesPostRequest: req });
  return response.data;
}

export async function cancelSchedule(id: string): Promise<void> {
  await defaultClient.apiCoreSchedulesScheduleIdCancelPost({ scheduleId: id });
}

export async function enableSchedule(id: string): Promise<Schedule> {
  const response = await defaultClient.apiCoreSchedulesScheduleIdEnablePost({ scheduleId: id });
  return response.data;
}

export async function runScheduleNow(id: string): Promise<{ task_id: string; conversation_id: string }> {
  const response = await defaultClient.apiCoreSchedulesScheduleIdRunNowPost({ scheduleId: id });
  return response.data;
}

export async function updateSchedule(id: string, req: Partial<CreateScheduleRequest>): Promise<Schedule> {
  const response = await notificationClient.apiCoreSchedulesScheduleIdPut({
    scheduleId: id,
    apiCoreSchedulesPostRequest: req,
  });
  return response.data;
}

export async function deleteSchedule(id: string): Promise<void> {
  await defaultClient.apiCoreSchedulesScheduleIdDelete({ scheduleId: id });
}

export async function listScheduleTasks(
  scheduleId: string,
  page: number,
  pageSize = 10,
): Promise<TaskListResponse> {
  const response = await defaultClient.apiCoreTaskCenterSchedulesScheduleIdTasksGet({ scheduleId, page, pageSize });
  return response.data as unknown as TaskListResponse;
}

export async function listAutomationGroups(): Promise<{ items: AutomationGroup[]; total: number }> {
  const response = await defaultClient.apiCoreAutomationGroupsGet();
  return response.data;
}

export async function createAutomationGroup(req: { name: string; remark?: string; timezone?: string }): Promise<AutomationGroup> {
  const response = await defaultClient.apiCoreAutomationGroupsPost({ automationGroupCreateRequest: req });
  return response.data;
}

export async function deleteAutomationGroup(id: string): Promise<void> {
  await defaultClient.apiCoreAutomationGroupsGroupIdDelete({ groupId: id });
}

export async function moveSchedule(id: string, groupId?: string, position = 0): Promise<void> {
  await defaultClient.apiCoreSchedulesScheduleIdMovePost(
    { scheduleId: id, scheduleMoveRequest: { group_id: groupId || null, position } },
  );
}

export interface BatchScheduleDraft {
  client_key: string;
  name: string;
  remark?: string;
  cron_expr: string;
  prompt_template: string;
  kb_ids?: string[];
  file_ids?: string[];
  dependencies?: Array<ScheduleDependency & { source_client_key?: string }>;
  notification?: NotificationUpdate;
}

export async function batchCreateAutomationGroup(req: {
  group: { name: string; remark?: string; timezone: string };
  tasks: BatchScheduleDraft[];
}): Promise<{ group_id: string; schedule_ids: Record<string, string> }> {
  const response = await notificationClient.apiCoreAutomationGroupsBatchCreatePost({
    automationGroupBatchCreateRequest: req,
  });
  return response.data;
}
