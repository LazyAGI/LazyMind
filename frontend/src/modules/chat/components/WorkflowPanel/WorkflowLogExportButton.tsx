import { useEffect, useRef, useState } from 'react';
import { Button, message } from 'antd';
import { useTranslation } from 'react-i18next';
import JSZip from '@progress/jszip-esm';
import { AgentAppsAuth } from '@/components/auth';
import { assistantBridgeJSON, AssistantBridgeRequestError } from '@/runtime/assistantSession';
import { downloadWorkflowFile, readEmbeddedWorkflowHostLogs, WorkflowDownloadCancelled } from '@/modules/chat/utils/workflowDownload';
import { WorkflowSessionApi, type LogRecord } from '@/modules/chat/utils/request';

export function WorkflowLogExportButton({ runId }: { runId: string }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const pending = useRef<AbortController>();
  useEffect(() => () => pending.current?.abort(), []);

  async function exportLogs() {
    if (pending.current) return;
    const identity = AgentAppsAuth.getSessionIdentity();
    const request = new AbortController();
    const cutoff = Date.now();
    pending.current = request;
    setBusy(true);
    const current = () => !request.signal.aborted && identity === AgentAppsAuth.getSessionIdentity();
    const missing: string[] = [];
    try {
      const response = await WorkflowSessionApi().getRunLogs(runId, cutoff, { signal: request.signal });
      const data = response.data.result;
      if (!data || data.run_id !== runId) throw new Error('Invalid workflow log response');
      if (!current()) return;
      const zip = new JSZip();
      function add(name: string, rows: LogRecord[]) {
        if (!rows.length) return false;
        const ordered = [...rows].sort((a, b) => Date.parse(a.time) - Date.parse(b.time));
        zip.file(name, ordered.map(row => JSON.stringify(row)).join('\n') + '\n');
        return true;
      }
      if (!add('lazymind.jsonl', data.scheduling)) missing.push(t('chat.workflowLogsNoScheduling'));
      if (data.host_required) {
        const providers: Record<string, string> = { codex: 'Codex', workbuddy: 'WorkBuddy', 'deepseek-harness': 'DSH' };
        const host = providers[data.host_provider ?? ''] || t('chat.workflowLogsExternalAgent');
        if (data.host_grant) {
          try {
            type HostLogs = { run_id: string; entries: LogRecord[] };
            const result = await (readEmbeddedWorkflowHostLogs<HostLogs>(data.host_grant, request.signal) ?? assistantBridgeJSON<HostLogs>(
              `/workflow-runs/${encodeURIComponent(runId)}/host-logs`, {
                method: 'POST', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ grant: data.host_grant }), signal: request.signal,
                credentials: 'omit', redirect: 'error', cache: 'no-store',
              }, 17000));
            if (result.run_id !== runId || !Array.isArray(result.entries)) throw new Error('Invalid host log response');
            if (!add('external-agent.jsonl', result.entries)) missing.push(t('chat.workflowLogsHostEmpty', { host }));
          } catch (error) {
            const key = error instanceof AssistantBridgeRequestError && error.code === 'LOG_HOST_NOT_PAIRED'
              ? 'chat.workflowLogsHostUnpaired' : 'chat.workflowLogsHostUnavailable';
            missing.push(t(key, { host }));
          }
        } else {
          missing.push(t('chat.workflowLogsHostUnpaired', { host }));
        }
      } else if (!add('native-agent.jsonl', data.agent)) {
        missing.push(t('chat.workflowLogsNoNativeAgent'));
      }
      if (!current()) return;
      if (Object.keys(zip.files).length === 0) {
        message.warning(missing.join(' · '));
        return;
      }
      zip.file('summary.txt', `Workflow run ID: ${runId}\nExport cutoff (UTC): ${data.cutoff}\n`);
      const blob = await zip.generateAsync({ type: 'blob', compression: 'DEFLATE' });
      if (!current()) return;
      await downloadWorkflowFile({ blob, filename: `workflow-logs-${runId}.zip` });
      if (missing.length && current()) message.warning(t('chat.workflowLogsPartial', { reason: missing.join(' · ') }));
    } catch (error) {
      if (!(error instanceof WorkflowDownloadCancelled) && current()) message.error(t('chat.workflowLogsExportFailed'));
    } finally {
      pending.current = undefined;
      if (!request.signal.aborted) setBusy(false);
    }
  }

  return <Button size='small' loading={busy} onClick={() => void exportLogs()}>{t('chat.workflowLogsExport')}</Button>;
}
