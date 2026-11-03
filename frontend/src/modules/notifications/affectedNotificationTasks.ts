import { listTasks, type Task } from '@/modules/taskCenter/api';
import { channels, getExecutionNotifications, type ChannelName, type NotificationConfig, type Preferences } from './api';

const activeTaskStatuses = new Set(['pending', 'running', 'waiting', 'waiting_inputs']);
const desktopTaskTypes = new Set(['workflow_run', 'background_chat']);
const pageSize = 100;
const maxPages = 100;

export async function affectedNotificationTasks(
  preferences: Preferences,
  next?: NotificationConfig,
): Promise<Array<{ task: Task; channels: ChannelName[] }>> {
  if (!preferences.enabled) return [];
  const closedChannels = channels.filter(channel => preferences.defaults.channels[channel]?.enabled
    && (!next || !next.channels[channel]?.enabled));
  // Event defaults apply only to newly created tasks, never existing runs.
  if (closedChannels.length === 0) return [];

  const desktopAffected = closedChannels.includes('desktop');
  const affected: Array<{ task: Task; channels: ChannelName[] }> = [];
  const seen = new Set<string>();
  for (let page = 1; page <= maxPages; page += 1) {
    const result = await listTasks({ page, page_size: pageSize });
    for (const task of result.items) {
      if (seen.has(task.id) || !activeTaskStatuses.has(task.status)) continue;
      seen.add(task.id);
      // Older responses identify scheduled runs by schedule_id alone. Explicit
      // ordinary task types retain their desktop-only notification semantics.
      const scheduled = task.task_type === 'scheduled' || (!task.task_type && Boolean(task.schedule_id));
      if (!scheduled) {
        if (desktopAffected && desktopTaskTypes.has(task.task_type)) affected.push({ task, channels: ['desktop'] });
        continue;
      }

      const { snapshot } = await getExecutionNotifications(task.id);
      const config = snapshot.config;
      const taskChannels: ChannelName[] = config
        ? closedChannels.filter(channel => config.channels[channel]?.enabled)
        : desktopAffected ? ['desktop'] : [];
      if (taskChannels.length > 0) affected.push({ task, channels: taskChannels });
    }
    if (page * pageSize >= result.total) return affected;
  }
  // Never present a partial list as the complete impact of a close operation.
  throw new Error('TOO_MANY_RUNS');
}
