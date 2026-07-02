/**
 * StateGraphModal — Modal container for the plugin workflow StateGraph.
 *
 * - Fetches GET /plugin-sessions/{sessionId}/state-graph on open.
 * - When liveRefresh=true, listens for plugin state-change events dispatched
 *   by the task-center SSE handler and re-fetches on each relevant event.
 * - Dagre layout is cached; only node statuses are replaced on refresh.
 */
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Modal, Spin } from 'antd';
import { axiosInstance, BASE_URL } from '@/components/request';
import StateGraphView, { type StateGraphData } from './StateGraphView';
import './index.scss';

export const PLUGIN_GRAPH_REFRESH_EVENT = 'plugin:graph:refresh';

/** Dispatch this event from the SSE handler to trigger a live graph refresh. */
export function dispatchGraphRefresh(conversationId: string) {
  window.dispatchEvent(
    new CustomEvent(PLUGIN_GRAPH_REFRESH_EVENT, { detail: { conversationId } }),
  );
}

export interface StateGraphModalProps {
  open: boolean;
  onClose: () => void;
  sessionId: string;
  pluginId: string;
  liveRefresh?: boolean;
  conversationId?: string;
}

const coreApiBase = `${BASE_URL}/api/core`;

export default function StateGraphModal({
  open,
  onClose,
  sessionId,
  liveRefresh = false,
  conversationId,
}: StateGraphModalProps) {
  const [data, setData] = useState<StateGraphData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Keep a cached layout anchor so dagre only re-runs when topology changes.
  const cachedDataRef = useRef<StateGraphData | null>(null);

  const fetchGraph = useCallback(async () => {
    if (!sessionId) return;
    setLoading((prev) => !prev ? true : prev);
    try {
      const resp = await axiosInstance.get(
        `${coreApiBase}/plugin-sessions/${encodeURIComponent(sessionId)}/state-graph`,
      );
      // ReplyOK wraps the payload as { code, message, data: {...} }.
      const incoming: StateGraphData = resp.data?.data ?? resp.data;
      // Merge: preserve topology reference if node/edge IDs haven't changed,
      // so StateGraphView's useMemo layout cache stays valid.
      if (cachedDataRef.current) {
        const prevIds = cachedDataRef.current.nodes.map((n) => n.id).join(',');
        const nextIds = incoming.nodes.map((n) => n.id).join(',');
        const prevEdges = cachedDataRef.current.edges.map((e) => `${e.from}-${e.to}`).join(',');
        const nextEdges = incoming.edges.map((e) => `${e.from}-${e.to}`).join(',');
        if (prevIds === nextIds && prevEdges === nextEdges) {
          // Same topology — only replace node status fields, keep array identity for layout.
          const updatedNodes = cachedDataRef.current.nodes.map((n) => {
            const updated = incoming.nodes.find((u) => u.id === n.id);
            return updated ?? n;
          });
          const merged: StateGraphData = {
            ...incoming,
            nodes: updatedNodes,
          };
          cachedDataRef.current = merged;
          setData(merged);
          setError(null);
          setLoading(false);
          return;
        }
      }
      cachedDataRef.current = incoming;
      setData(incoming);
      setError(null);
    } catch {
      setError('加载工作流图失败');
    } finally {
      setLoading(false);
    }
  }, [sessionId]);

  // Fetch on open.
  useEffect(() => {
    if (open && sessionId) {
      void fetchGraph();
    } else if (!open) {
      // Reset on close so next open shows fresh state.
      setData(null);
      cachedDataRef.current = null;
      setError(null);
    }
  }, [open, sessionId, fetchGraph]);

  // Live refresh — listen for graph refresh events dispatched by SSE handler.
  useEffect(() => {
    if (!open || !liveRefresh || !conversationId) return;
    function handler(e: Event) {
      const detail = (e as CustomEvent<{ conversationId: string }>).detail;
      if (!detail || detail.conversationId !== conversationId) return;
      void fetchGraph();
    }
    window.addEventListener(PLUGIN_GRAPH_REFRESH_EVENT, handler);
    return () => window.removeEventListener(PLUGIN_GRAPH_REFRESH_EVENT, handler);
  }, [open, liveRefresh, conversationId, fetchGraph]);

  // Compute Modal width based on node count: more nodes → wider modal.
  const modalWidth = (() => {
    if (!data) return 900;
    const nonTerminal = (data.nodes ?? []).filter((n) => n.id !== '__start__' && n.id !== '__end__').length;
    // Each node is ~148px wide + ~42px gap; clamp between 700 and min(1200, viewport*0.9).
    const estimated = 88 + nonTerminal * (148 + 42) + 88;
    return Math.min(1200, Math.max(700, estimated));
  })();

  return (
    <Modal
      open={open}
      onCancel={onClose}
      footer={null}
      title='工作流图'
      width={modalWidth}
      style={{ top: 40 }}
      className='state-graph-modal'
      destroyOnClose
    >
      <div className='state-graph-modal__content'>
        {loading && !data && (
          <div className='state-graph-modal__loading'>
            <Spin tip='加载中…' />
          </div>
        )}
        {error && !data && (
          <div className='state-graph-modal__error'>{error}</div>
        )}
        {data && <StateGraphView data={data} />}
      </div>
    </Modal>
  );
}
