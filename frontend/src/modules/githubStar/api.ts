import { BASE_URL, axiosInstance } from '@/components/request';
import { unwrapApiData } from '@/modules/dataSource/api/unwrap';
import type { Task } from '@/modules/taskCenter/api';

const starPath = `${BASE_URL}/api/authservice/v1/cloud/github/star`;
const options = { silentError: true, timeout: 60_000 };
export const GITHUB_STAR_OPPORTUNITY_EVENT = 'lazymind:github-star-opportunity';

export interface GitHubStarOpportunity {
  task_id: string;
  conversation_id: string;
  status: string;
  result_ready: boolean;
  source: 'final_result' | 'stage';
}

export interface GitHubStarAccount {
  connection_id: string;
  account_id: string;
  login: string;
  state: 'starred' | 'not_starred' | 'unknown';
  eligible: boolean;
}

export interface GitHubStarResult {
  repository: string;
  connection_id: string;
  account_id: string;
  login: string;
  state: 'starred';
  write_performed: boolean;
}

export interface LocalGitHubStarResult {
  repository: string;
  account_id: string;
  login: string;
  state: 'starred' | 'connection_required';
  write_performed: boolean;
}

export interface LocalGitHubStarCapability {
  repository: string;
  eligible: boolean;
}

export interface GitHubStarClaim {
  show: boolean;
  reason: string;
  task_id: string;
  min_successful_tasks: number;
  cooldown_days: number;
  max_prompts: number;
}

export async function readGitHubStarAccounts(): Promise<{ repository: string; accounts: GitHubStarAccount[]; can_star?: boolean }> {
  const response = await axiosInstance.get(starPath, options);
  return unwrapApiData(response.data);
}

export async function claimGitHubStarPrompt(opportunity: GitHubStarOpportunity): Promise<GitHubStarClaim> {
  const response = await axiosInstance.post(`${BASE_URL}/api/core/task-center/github-star-prompt/claim`, { task_id: opportunity.task_id }, options);
  return unwrapApiData(response.data);
}

export async function writeGitHubStar(body: {
  connection_id: string; expected_account_id: string; confirmed: true;
}): Promise<GitHubStarResult> {
  const response = await axiosInstance.put(starPath, body, options);
  return unwrapApiData(response.data);
}

export async function writeLocalGitHubStar(body: { confirmed: true }): Promise<LocalGitHubStarResult> {
  const response = await axiosInstance.put(`${starPath}/local`, body, options);
  return unwrapApiData(response.data);
}

export async function readLocalGitHubStarCapability(): Promise<LocalGitHubStarCapability> {
  const response = await axiosInstance.get(`${starPath}/local`, options);
  return unwrapApiData(response.data);
}

export async function markGitHubStarSupported(): Promise<void> {
  await axiosInstance.put(`${BASE_URL}/api/core/user/github-star-prompt/support`, { supported: true }, options);
}

export async function listGitHubStarWorkTasks(conversationId: string, signal?: AbortSignal): Promise<Task[]> {
  const response = await axiosInstance.get(`${BASE_URL}/api/core/task-center/tasks`, {
    ...options, signal, params: { conversation_id: conversationId, page_size: 100 },
  });
  const page = unwrapApiData<{ items: Task[] }>(response.data);
  return page.items.filter(task => task.conversation_id === conversationId
    && ['workflow_run', 'background_chat', 'scheduled'].includes(task.task_type));
}

// A completion must match a root observed running in this Work episode.
export async function findGitHubStarOpportunity(conversationId: string, workflowSessionId?: string, taskIds?: readonly string[]): Promise<GitHubStarOpportunity | null> {
  const tasks = await listGitHubStarWorkTasks(conversationId);
  if (tasks.some(task => task.status === 'running' || task.status === 'waiting')) return null;
  const root = tasks.find(task => task.status === 'succeeded' && (!taskIds || taskIds.includes(task.id))
    && (!workflowSessionId || task.workflow_session_id === workflowSessionId));
  return root ? { task_id: root.id, conversation_id: conversationId,
    status: 'succeeded', result_ready: true, source: 'final_result' } : null;
}
