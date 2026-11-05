import { useEffect, useRef, useState } from 'react';
import { CheckCircleOutlined, ClockCircleOutlined, DownOutlined, LoadingOutlined, WarningOutlined } from '@ant-design/icons';
import { AgentAppsAuth } from '@/components/auth';
import { axiosInstance, BASE_URL } from '@/components/request';
import { Method, SSE } from '@/modules/chat/utils/sse';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import './PaperAnalysisProgress.scss';

type Output = { text: string; estimated_tokens: number; format: 'json' | 'python' | 'text'; mode: 'streaming' | 'buffered' | 'complete' | 'interrupted'; truncated?: boolean };
type Step = { step_id: string; order: number; title: string; status: string; summary?: string;
  started_at: string | null; finished_at: string | null; elapsed_ms: number | null; output?: Output; reasoning?: Output };
type Snapshot = { task_id: string; execution_id: string; revision: number; status: string; process_steps: Step[];
  error?: { code: string; field?: string; message: string } | null;
  timing: { started_at: string | null; finished_at: string | null; measured_at: string }; };
const terminal = new Set(['succeeded', 'failed', 'canceled', 'interrupted']);
const knownStatus = new Set(['pending', 'running', ...terminal]);

function readOutput(output: Output | undefined, reasoning = false): Output | undefined {
  return output && typeof output.text === 'string' && output.text.length <= (reasoning ? 32 : 128) * 1024
    && (reasoning ? output.format === 'text' : ['json', 'python', 'text'].includes(output.format))
    && ['streaming', 'buffered', 'complete', 'interrupted'].includes(output.mode) ? output : undefined;
}

export function readPaperProgress(value: unknown, taskId: string): Snapshot | null {
  if (!value || typeof value !== 'object') return null;
  const data = value as Snapshot;
  if (data.task_id !== taskId || typeof data.execution_id !== 'string' || !Number.isFinite(data.revision)
      || !knownStatus.has(data.status) || !Array.isArray(data.process_steps) || !data.timing) return null;
  const steps = data.process_steps.filter(step => typeof step?.step_id === 'string' && step.step_id.startsWith('f4-')
    && typeof step.title === 'string' && typeof step.order === 'number' && knownStatus.has(step.status)).map(step => {
    return { ...step, output: readOutput(step.output), reasoning: readOutput(step.reasoning, true) };
  });
  const error = data.error && typeof data.error.code === 'string' && typeof data.error.message === 'string'
    ? { ...data.error, message: data.error.message.slice(0, 1200) } : null;
  return { ...data, error, process_steps: steps.sort((a, b) => a.order - b.order) };
}

function duration(ms: number) {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  return seconds < 60 ? `${seconds} 秒` : `${Math.floor(seconds / 60)} 分 ${String(seconds % 60).padStart(2, '0')} 秒`;
}

function ModelOutput({ output, reasoning = false }: { output: Output; reasoning?: boolean }) {
  const [visible, setVisible] = useState(!reasoning && output.mode === 'buffered' ? '' : output.text);
  const [reasoningOpen, setReasoningOpen] = useState(false);
  const body = useRef<HTMLPreElement>(null);
  const follow = useRef(true);
  const label = reasoning ? `模型推理 · ${output.mode === 'streaming' ? '正在返回' : output.mode === 'interrupted' ? '已中断' : '已返回'}`
    : output.mode === 'streaming' ? '模型正在返回内容' : output.mode === 'buffered'
    ? '结果已返回，分段展示' : output.mode === 'interrupted' ? '本次返回已中断' : '模型返回内容 · 仍以最终校验结果为准';
  useEffect(() => {
    if (reasoning || output.mode !== 'buffered' || window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      setVisible(output.text); return;
    }
    let frame: number;
    let count = 0;
    const chunk = Math.max(256, Math.ceil(output.text.length / 36));
    const next = () => {
      count = Math.min(output.text.length, count + chunk);
      setVisible(output.text.slice(0, count));
      if (count < output.text.length) frame = window.requestAnimationFrame(next);
    };
    frame = window.requestAnimationFrame(next);
    return () => window.cancelAnimationFrame(frame);
  }, [output.text, output.mode, reasoning]);
  useEffect(() => { if (body.current && follow.current) body.current.scrollTop = body.current.scrollHeight; }, [visible]);
  return <details className='paper-f4-progress__output' open={reasoning ? reasoningOpen : output.mode === 'streaming'}
    onToggle={reasoning ? event => setReasoningOpen(event.currentTarget.open) : undefined}>
    <summary>{label}<span>{output.format.toUpperCase()} · 约 {output.estimated_tokens.toLocaleString()} tokens{output.truncated ? ' · 仅展示部分内容' : ''}</span></summary>
    <pre ref={body} onScroll={event => {
      const element = event.currentTarget;
      follow.current = element.scrollHeight - element.scrollTop - element.clientHeight < 30;
    }}><code>{visible}</code></pre>
  </details>;
}

/** F4-only public execution view; no raw task/debug endpoint is requested. */
export function PaperAnalysisProgress({ session, active = true }: { session: WorkflowSession; active?: boolean }) {
  const step = (session.steps ?? []).filter(item => item.step_id === 'data_analysis' && item.validity !== 'stale')
    .slice().sort((a, b) => b.attempt - a.attempt || Date.parse(b.created_at) - Date.parse(a.created_at))[0];
  const taskId = step?.task_id;
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [connected, setConnected] = useState(true);
  const [now, setNow] = useState(Date.now());
  const [reload, setReload] = useState(0);
  const offset = useRef(0);
  useEffect(() => {
    if (!taskId || !active) return;
    let alive = true;
    let stream: SSE | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let loaded: Snapshot | null = null;
    setSnapshot(null); setConnected(true);
    const endpoint = `${BASE_URL}/api/chat/paper-f4/tasks/${encodeURIComponent(taskId)}/progress`;
    const apply = (raw: unknown) => {
      const value = readPaperProgress(raw, taskId);
      if (!alive || !value || loaded && value.execution_id === loaded.execution_id && value.revision < loaded.revision) return;
      loaded = value;
      const measured = Date.parse(value.timing.measured_at);
      if (Number.isFinite(measured)) offset.current = measured - Date.now();
      setSnapshot(value); setNow(Date.now() + offset.current); setConnected(true);
    };
    const load = async () => {
      const response = await axiosInstance.get(endpoint, { headers: AgentAppsAuth.getAuthHeaders() });
      apply(response.data);
    };
    const connect = () => {
      if (!alive) return;
      stream?.close();
      stream = new SSE(`${endpoint}?stream=1`, { method: Method.GET,
        headers: { Accept: 'text/event-stream', ...AgentAppsAuth.getAuthHeaders() }, timeout: 3600000,
        callbacks: {
          message: (event: CustomEvent) => {
            const raw = (event as unknown as { data?: string }).data;
            if (!alive || !raw || raw === '[DONE]') return;
            try {
              const data = JSON.parse(raw);
              if (data.type === 'task_snapshot') apply(data.data);
              if (data.type === 'resync_required') void load().catch(() => { if (alive) setConnected(false); });
            } catch { /* Ignore malformed transport frames; the last valid snapshot stays visible. */ }
          },
          error: () => {
            if (!alive || loaded && terminal.has(loaded.status)) return;
            setConnected(false); stream?.close();
            if (!retry) retry = setTimeout(() => { retry = undefined; void load().catch(() => {}); connect(); }, 3000);
          },
        },
      });
    };
    void load().catch(() => { if (alive) setConnected(false); });
    connect();
    return () => { alive = false; stream?.close(); if (retry) clearTimeout(retry); };
  }, [taskId, active, reload]);
  const view = snapshot?.task_id === taskId ? snapshot : null;
  const status = view?.status ?? step?.status ?? 'pending';
  const running = !terminal.has(status);
  useEffect(() => {
    if (!active || !running) return;
    const timer = setInterval(() => setNow(Date.now() + offset.current), 1000);
    return () => clearInterval(timer);
  }, [active, running]);
  if (!step) return null;
  const steps = view?.process_steps ?? [];
  const current = [...steps].reverse().find(item => item.status === 'running');
  const completed = steps.filter(item => item.status === 'succeeded');
  const latest = steps[steps.length - 1];
  const start = Date.parse(view?.timing.started_at ?? step.created_at);
  const end = Date.parse(view?.timing.finished_at ?? '') || now;
  const currentWait = current?.started_at ? now - Date.parse(current.started_at) : 0;
  const retrying = !!current?.summary?.includes('连接已断开');
  const label = status === 'succeeded' ? '分析完成，产物已保存' : status === 'failed' ? '本次分析未完成'
    : status === 'canceled' || status === 'interrupted' ? '本次分析已停止' : current?.title ?? (steps.length ? '正在确认阶段结果' : '正在启动数据分析');
  const Mark = status === 'succeeded' ? CheckCircleOutlined : status === 'failed' || retrying ? WarningOutlined : LoadingOutlined;
  return <section className={`paper-f4-progress is-${status}`} aria-label='数据分析执行进度'>
    <div className='paper-f4-progress__overview'>
      <div className='paper-f4-progress__heading'><Mark /><strong role='status' aria-live='polite'>{label}</strong>
        <span>总耗时 {duration(Number.isFinite(start) ? end - start : 0)}</span></div>
      {running && current && <p className='paper-f4-progress__wait'>本阶段已用时 {duration(currentWait)}{current.summary ? ` · ${current.summary}` : ''}</p>}
      <p className='paper-f4-progress__confirmed'>{completed.length ? `已完成 ${completed.length} 个执行阶段` : '等待阶段记录'}
        {completed.find(item => item.summary?.startsWith('计划已确定'))?.summary && ` · ${completed.find(item => item.summary?.startsWith('计划已确定'))!.summary}`}</p>
      {!connected && <div className='paper-f4-progress__notice' role='status'>进度连接暂时中断，正在重连；执行状态尚待确认。<button type='button' onClick={() => setReload(value => value + 1)}>重新连接</button></div>}
      {connected && running && currentWait >= 60000 && !current?.output?.text && !retrying && <div className='paper-f4-progress__notice'><ClockCircleOutlined />{current?.summary?.startsWith('已收到推理片段')
        ? '已收到模型的推理信号，尚未返回正文；任务仍在运行。'
        : '尚未收到可展示的正文，任务仍在运行，可稍后回来查看。'}</div>}
      {!running && (view?.error || latest?.status === 'failed') && <p className='paper-f4-progress__notice' role='alert'>{view?.error?.message || latest?.summary}</p>}
      {running && !expanded && current?.reasoning?.text && <ModelOutput key={`${current.step_id}-reasoning`} output={current.reasoning} reasoning />}
      {running && !expanded && current?.output && <ModelOutput key={current.step_id} output={current.output} />}
    </div>
    <details open={expanded} onToggle={event => setExpanded(event.currentTarget.open)}>
      <summary className='paper-f4-progress__toggle'><span>{running ? '查看当前执行过程' : '查看执行记录'}</span><DownOutlined rotate={expanded ? 180 : 0} /></summary>
      <ol className='paper-f4-progress__timeline'>
        {steps.map(item => <li key={item.step_id} className={`is-${item.status}`}>
          <span className='paper-f4-progress__marker'>{item.status === 'succeeded' ? <CheckCircleOutlined /> : item.status === 'running' ? <LoadingOutlined /> : <WarningOutlined />}</span>
          <div><div className='paper-f4-progress__step-heading'><strong>{item.title}</strong><span>{item.elapsed_ms != null ? duration(item.elapsed_ms) : item.started_at ? `已等待 ${duration(now - Date.parse(item.started_at))}` : ''}</span></div>
            {item.summary && <p>{item.summary}</p>}
            {item.reasoning?.text && (!running || item.step_id !== current?.step_id || expanded) && <ModelOutput output={item.reasoning} reasoning />}
            {item.output && (!running || item.step_id !== current?.step_id || expanded) && <ModelOutput output={item.output} />}
          </div>
        </li>)}
        {!steps.length && <li>当前任务尚无详细阶段记录。</li>}
      </ol>
    </details>
  </section>;
}
