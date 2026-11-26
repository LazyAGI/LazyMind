import { useEffect, useReducer, useState } from 'react';
import type { TFunction } from 'i18next';
import { AGENTS } from '@/modules/agentIntegration/catalog';
import { ConversationSettingsApi, type ChatExecutorDescriptor } from '../utils/request';

export function buildExecutorCatalog(
  executors: ChatExecutorDescriptor[],
  selectedId: string,
  unavailableReason: string,
): ChatExecutorDescriptor[] {
  const live = new Map(executors.map(executor => [executor.id, executor]));
  const catalog: ChatExecutorDescriptor[] = [{
    id: 'lazymind', display_name: 'LazyMind', kind: 'internal',
    available: true, installed: true, host_online: true,
  }];
  for (const agent of AGENTS) {
    const status = live.get(agent.id);
    catalog.push(status && agent.executorName ? {
      ...status, display_name: agent.name,
      available: status.available === true && status.host_online === true && status.installed === true,
    } : {
      id: agent.id, display_name: agent.name, kind: 'external',
      available: false, installed: false, host_online: false,
      unavailable_reason_code: agent.executorName ? 'unavailable' : 'inbound_only',
      unavailable_reason: unavailableReason,
    });
  }
  const known = new Set(catalog.map(executor => executor.id));
  for (const executor of live.values()) {
    if (!known.has(executor.id)) catalog.push(executor);
  }
  if (selectedId && !known.has(selectedId) && !live.has(selectedId)) {
    catalog.push({
      id: selectedId, display_name: selectedId, kind: 'external',
      available: false, installed: false, host_online: false,
      unavailable_reason_code: 'unavailable', unavailable_reason: unavailableReason,
    });
  }
  return catalog;
}

function unavailableReasonCode(executor: ChatExecutorDescriptor): ChatExecutorDescriptor['unavailable_reason_code'] {
  if (executor.unavailable_reason_code) return executor.unavailable_reason_code;
  const reason = (executor.unavailable_reason ?? '').trim().toLowerCase();
  if (reason === 'disabled in lazymind settings') return 'not_activated';
  if (reason === 'dsh default model unavailable') return 'model_unavailable';
  if (reason === 'dsh execution unavailable') return 'unavailable';
  if (reason === 'dsh sign-in required' || / is not signed in;| sign-in has expired;/.test(reason)) return 'login_required';
  if (!executor.host_online) return 'host_offline';
  if (!executor.installed && reason.includes(' is not installed')) return 'not_installed';
  return undefined;
}

export function executorUnavailableReason(executor: ChatExecutorDescriptor, t: TFunction): string {
  if (executor.available) return '';
  const code = unavailableReasonCode(executor);
  if (code) {
    return t(`chat.executorUnavailableReason.${code}`, {
      defaultValue: executor.unavailable_reason || t('chat.executorUnavailableReason.unavailable'),
    });
  }
  return executor.unavailable_reason || t('chat.executorUnavailableReason.unavailable');
}

export const CHAT_EXECUTORS_CHANGED_EVENT = 'lazymind:chat-executors-changed';

export function notifyChatExecutorsChanged() {
  window.dispatchEvent(new Event(CHAT_EXECUTORS_CHANGED_EVENT));
}

export function useChatExecutors(active: boolean) {
  const [revision, refresh] = useReducer((value: number) => value + 1, 0);
  const [executors, setExecutors] = useState<ChatExecutorDescriptor[]>([]);
  const [loading, setLoading] = useState(active);
  const [error, setError] = useState(false);

  useEffect(() => {
    if (!active) {
      setExecutors([]);
      setLoading(false);
      setError(false);
      return;
    }
    const controller = new AbortController();
    let timer: number | undefined;
    const load = async () => {
      try {
        const response = await ConversationSettingsApi().listChatExecutors({
          silentError: true, signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        const values = response.data.data.executors;
        setExecutors(values.filter(item => item && typeof item.id === 'string' && typeof item.display_name === 'string'));
        setError(false);
      } catch {
        if (controller.signal.aborted) return;
        setExecutors([]);
        setError(true);
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
          timer = window.setTimeout(load, 3000);
        }
      }
    };
    setLoading(true);
    setError(false);
    setExecutors([]);
    void load();
    window.addEventListener(CHAT_EXECUTORS_CHANGED_EVENT, refresh);
    window.addEventListener('focus', refresh);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
      window.removeEventListener(CHAT_EXECUTORS_CHANGED_EVENT, refresh);
      window.removeEventListener('focus', refresh);
    };
  }, [active, revision]);
  return { executors, loading, error, refresh };
}
