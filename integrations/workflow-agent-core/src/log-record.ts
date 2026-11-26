export interface HostLogRead {
  id: string
  run_id: string
  driver_session_id: string
  cutoff_ms: number
  results_cutoff_ms?: number
  started_ms: number
}

export interface HostLogEntry {
  time: string
  event: 'agent.turn' | 'tool.call' | 'tool.result'
  tool?: string
  status?: 'running' | 'completed' | 'failed' | 'interrupted'
  duration_ms?: number
}

/** Also serialized into desktop renderers. Extract only fields safe for export. */
export function hostLogEntry(event: HostLogEntry['event'], time: unknown, cutoff: number, status?: unknown,
  tool?: unknown, duration?: unknown): HostLogEntry | undefined {
  const stamp = typeof time === 'number' ? time : typeof time === 'string' ? Date.parse(time) : NaN
  if (!Number.isFinite(stamp) || stamp <= 0 || stamp > cutoff) return
  const row: HostLogEntry = { time: new Date(stamp).toISOString(), event }
  if (typeof tool === 'string' && /^[a-zA-Z0-9_][a-zA-Z0-9_.:-]{0,127}$/.test(tool)) row.tool = tool
  switch (status) {
    case 'in_progress': case 'inProgress': case 'working': case 'running': row.status = 'running'; break
    case 'completed': case 'success': case 'succeeded': row.status = 'completed'; break
    case 'failed': case 'error': row.status = 'failed'; break
    case 'cancelled': case 'canceled': case 'interrupted': row.status = 'interrupted'; break
  }
  if (typeof duration === 'number' && Number.isFinite(duration) && duration >= 0) row.duration_ms = Math.round(duration)
  return row
}
