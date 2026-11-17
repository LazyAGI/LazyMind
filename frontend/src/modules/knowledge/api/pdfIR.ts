import i18n from "@/i18n";
import { useCallback, useEffect, useRef, useState } from 'react';
import { axiosInstance, BASE_URL, getLocalizedErrorMessage } from '@/components/request';

export interface PdfIRTarget { dataset: string; document: string; canWrite?: boolean }
export interface PdfIRTableCell { text: string; row_span: number; col_span: number; header?: boolean }
export interface PdfIRTable {
  rows: Array<{cells: PdfIRTableCell[]}>;
  captions: string[];
  footnotes: string[];
  number?: string;
  related_blocks: Array<{block_id: string; relation: 'caption' | 'mention'}>;
  warnings?: string[];
}
export interface PdfIRImage {captions:string[];footnotes:string[];number?:string;related_blocks:Array<{block_id:string;relation:"caption"|"mention"}>;warnings?:string[]}
export interface PdfIRBlock { id: string; type: string; page: number; bbox?: [number, number, number, number]; latex?: string; text?: string; table?: PdfIRTable; image?: PdfIRImage }
export interface PdfIRDocument {
  version: 1; projection_version?: number; source_sha256: string;
  parser: { name: string; model: string };
  coordinate_space: 'normalized_top_left';
  pages: Array<{index: number; width?: number; height?: number}>;
  blocks: PdfIRBlock[]; warnings?: string[];
}
export interface PdfIRState {
  status: 'not_requested' | 'queued' | 'running' | 'ready' | 'failed' | 'unavailable';
  source_sha256?: string; error_code?: string; error_message?: string; document?: PdfIRDocument;
}
export function usablePdfIR(state: PdfIRState, sourceHash?: string): PdfIRDocument | undefined {
  const doc = state.document;
  if (!sourceHash || doc?.version !== 1 || doc.coordinate_space !== 'normalized_top_left' || doc.source_sha256 !== sourceHash || (state.source_sha256 && state.source_sha256 !== sourceHash)) return;
  return doc;
}
export function pdfIRQuotaWaiting(state?: { status: string; error_code?: string }): boolean {
  return state?.status === 'queued' && state.error_code === 'reading_quota_exhausted';
}
const pdfIRUrl = (dataset: string, document: string) =>
  `${BASE_URL}/api/core/datasets/${encodeURIComponent(dataset)}/documents/${encodeURIComponent(document)}/pdf-ir`;

export async function getPdfIR(dataset: string, document: string, signal?: AbortSignal): Promise<PdfIRState> {
  const response = await axiosInstance.get<{ data: PdfIRState }>(pdfIRUrl(dataset, document), { signal, silentError: true } as never);
  return response.data.data;
}

class PdfIRSubmissionError extends Error {}
export function pdfIRSubmissionMessage(error: unknown): string {
  return error instanceof PdfIRSubmissionError ? error.message : getLocalizedErrorMessage(error);
}
export async function requestPdfIR(dataset: string, document: string, force: boolean): Promise<PdfIRState> {
  try {
    const response = await axiosInstance.post<{ data: PdfIRState }>(pdfIRUrl(dataset, document), { force }, { silentError: true } as never);
    const state = response.data.data;
    if (state.status === 'unavailable') throw new PdfIRSubmissionError(state.error_message || 'PDF 解析服务不可用，请检查 MinerU 配置后重试。');
    return state;
  } catch (error) {
    const status = (error as { response?: { status?: number } } | undefined)?.response?.status;
    if (status === 403)
      throw new PdfIRSubmissionError('无法提交 PDF 解析，请检查编辑权限和 AI 阅读准备设置。');
    if (status === 404)
      throw new PdfIRSubmissionError('PDF 原文不可用，或当前没有文档编辑权限。');
    throw error;
  }
}

const initial: PdfIRState = {status: 'not_requested'};
export function usePdfIR(target?: PdfIRTarget) {
  const dataset = target?.dataset, document = target?.document;
  const key = dataset && document ? JSON.stringify([dataset, document]) : '';
  const [result, setResult] = useState<{key: string; state: PdfIRState}>({key:'',state:initial});
  const [revision, setRevision] = useState(0);
  const currentKey = useRef(key);
  currentKey.current = key;
  // External preparation may create artifacts after terminal observation stopped.
  const refresh = useCallback(() => {
    if (key && currentKey.current === key) setRevision(value => value + 1);
  }, [key]);
  const [starting, setStarting] = useState(false);
  const pollRequest = useRef<AbortController>();
  const pollTimer = useRef<ReturnType<typeof setTimeout>>();
  const startRequest = useRef<AbortController>();
  const url = pdfIRUrl(dataset || '', document || '');
  useEffect(() => {
    const controller = new AbortController();
    pollRequest.current = controller;
    setStarting(false);
    if (dataset && document) {
      const poll = async () => {
        try {
          const state = await getPdfIR(dataset, document, controller.signal);
          if (controller.signal.aborted) return;
          setResult({key,state});
          if (state.status === 'queued' || state.status === 'running') pollTimer.current = setTimeout(poll, pdfIRQuotaWaiting(state) ? 30_000 : 2_000);
        } catch {
          if (!controller.signal.aborted) setResult(previous => ({key,state:{
            status:'failed', error_message:i18n.t("pdfFormula.readError"),
            document: previous.key === key ? previous.state.document : undefined,
          }}));
        }
      };
      void poll();
    }
    return () => { controller.abort(); startRequest.current?.abort(); clearTimeout(pollTimer.current); };
  }, [key, dataset, document, revision]);
  const start = useCallback(async (force = false) => {
    if (!key || target?.canWrite === false || startRequest.current && !startRequest.current.signal.aborted) return;
    pollRequest.current?.abort();
    clearTimeout(pollTimer.current);
    const controller = new AbortController();
    startRequest.current = controller;
    setStarting(true);
    try {
      const response = await axiosInstance.post<{data: PdfIRState}>(url, {force}, {signal:controller.signal,silentError:true} as never);
      if (!controller.signal.aborted) {
        const state = response.data.data;
        setResult(previous => ({ key, state: { ...state, document: state.document || (previous.key === key ? previous.state.document : undefined) } }));
        if (state.status === "queued" || state.status === "running") setRevision(v=>v+1);
      }
    } catch {
      if (!controller.signal.aborted) setResult(previous => ({key,state:{
        status:'failed', error_message:i18n.t("pdfFormula.startError"),
        document: previous.key === key ? previous.state.document : undefined,
      }}));
    } finally {
      if (!controller.signal.aborted) setStarting(false);
      if (startRequest.current === controller) startRequest.current = undefined;
    }
  }, [key,url,target?.canWrite]);
  return {state: result.key === key ? result.state : initial, loading: !!key && result.key !== key, starting, start, refresh};
}
