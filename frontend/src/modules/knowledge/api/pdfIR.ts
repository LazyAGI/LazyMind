import i18n from "@/i18n";
import { useCallback, useEffect, useRef, useState } from 'react';
import { axiosInstance, BASE_URL } from '@/components/request';

export interface PdfIRTarget { dataset: string; document: string; canWrite?: boolean }
export interface PdfIRBlock { id: string; type: string; page: number; bbox?: [number, number, number, number]; latex?: string; text?: string }
export interface PdfIRDocument {
  version: 1; source_sha256: string;
  parser: { name: string; model: string };
  coordinate_space: 'normalized_top_left';
  pages: Array<{index: number; width?: number; height?: number}>;
  blocks: PdfIRBlock[]; warnings?: string[];
}
export interface PdfIRState {
  status: 'not_requested' | 'queued' | 'running' | 'ready' | 'failed' | 'unavailable';
  source_sha256?: string; error_message?: string; document?: PdfIRDocument;
}
export function usablePdfIR(state: PdfIRState, sourceHash?: string): PdfIRDocument | undefined {
  const doc = state.document;
  if (state.status !== 'ready' || !sourceHash || doc?.version !== 1 || doc.coordinate_space !== 'normalized_top_left' || doc.source_sha256 !== sourceHash || (state.source_sha256 && state.source_sha256 !== sourceHash)) return;
  return doc;
}
const initial: PdfIRState = {status: 'not_requested'};
export function usePdfIR(target?: PdfIRTarget) {
  const dataset = target?.dataset, document = target?.document;
  const key = dataset && document ? `${dataset}/${document}` : '';
  const [result, setResult] = useState<{key: string; state: PdfIRState}>({key:'',state:initial});
  const [revision, setRevision] = useState(0);
  const [starting, setStarting] = useState(false);
  const pollRequest = useRef<AbortController>();
  const pollTimer = useRef<ReturnType<typeof setTimeout>>();
  const startRequest = useRef<AbortController>();
  const url = `${BASE_URL}/api/core/datasets/${encodeURIComponent(dataset || '')}/documents/${encodeURIComponent(document || '')}/pdf-ir`;
  useEffect(() => {
    const controller = new AbortController();
    pollRequest.current = controller;
    setStarting(false);
    if (key) {
      const poll = async () => {
        try {
          const response = await axiosInstance.get<{data: PdfIRState}>(url, {signal: controller.signal, silentError: true} as never);
          if (controller.signal.aborted) return;
          const state = response.data.data;
          setResult({key,state});
          if (state.status === 'queued' || state.status === 'running') pollTimer.current = setTimeout(poll, 2000);
        } catch {
          if (!controller.signal.aborted) setResult({key,state:{status:'failed',error_message:i18n.t("pdfFormula.readError")}});
        }
      };
      void poll();
    }
    return () => { controller.abort(); startRequest.current?.abort(); clearTimeout(pollTimer.current); };
  }, [key, url, revision]);
  const start = useCallback(async (force = false) => {
    if (!key || startRequest.current && !startRequest.current.signal.aborted) return;
    pollRequest.current?.abort();
    clearTimeout(pollTimer.current);
    const controller = new AbortController();
    startRequest.current = controller;
    setStarting(true);
    try {
      const response = await axiosInstance.post<{data: PdfIRState}>(url, {force}, {signal:controller.signal,silentError:true} as never);
      if (!controller.signal.aborted) { setResult({key,state:response.data.data}); if (response.data.data.status === "queued" || response.data.data.status === "running") setRevision(v=>v+1); }
    } catch {
      if (!controller.signal.aborted) setResult({key,state:{status:'failed',error_message:i18n.t("pdfFormula.startError")}});
    } finally {
      if (!controller.signal.aborted) setStarting(false);
      if (startRequest.current === controller) startRequest.current = undefined;
    }
  }, [key,url]);
  return {state: result.key === key ? result.state : initial, starting, start};
}
