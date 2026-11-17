import { useCallback, useEffect, useRef, useState } from 'react';
import type { PdfStructureStatus } from '../api/pdfArtifacts';
import { getPdfIR, requestPdfIR, pdfIRQuotaWaiting, type PdfIRState } from '../api/pdfIR';
import PdfParseStatus from './PdfParseStatus';
import type { ParseState } from './model';

const badgeStates: Record<PdfStructureStatus['status'], ParseState> = {
  not_requested: 'not_requested', queued: 'queued', running: 'parsing', ready: 'ready',
  failed: 'failed', unavailable: 'unknown', unknown: 'unknown',
};

export default function DocumentPdfParseStatus({ dataset, document, ...props }: {
  dataset: string;
  document: string;
  initial?: PdfStructureStatus;
  fallback?: ParseState;
  fallbackError?: string;
  disabledReason?: string;
  observeOnMount?: boolean;
  onSubmit?: (document: string, force: boolean) => Promise<PdfIRState>;
}) {
  return <BoundStatus key={JSON.stringify([dataset, document])} dataset={dataset} document={document} {...props} />;
}

function BoundStatus({ dataset, document, initial, fallback, fallbackError, disabledReason, observeOnMount = true, onSubmit }: Parameters<typeof DocumentPdfParseStatus>[0]) {
  const [observed, setObserved] = useState<{ value: PdfStructureStatus; initial?: PdfStructureStatus }>();
  const structure = observed && (observed.initial === initial || !initial) ? observed.value : initial;
  const structureRef = useRef(structure);
  structureRef.current = structure;
  const [open, setOpen] = useState(false);
  const read = useRef<AbortController>();
  const submitting = useRef(false);
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; read.current?.abort(); };
  }, []);
  const refresh = useCallback(async () => {
    if (submitting.current) return;
    read.current?.abort();
    const controller = new AbortController();
    read.current = controller;
    try {
      const { document: retainedDocument, ...state } = await getPdfIR(dataset, document, controller.signal);
      if (!controller.signal.aborted)
        setObserved({ value: { ...state, available: !!retainedDocument }, initial });
    } catch (error) {
      if (!controller.signal.aborted) throw error;
    } finally {
      if (read.current === controller) read.current = undefined;
    }
  }, [dataset, document, initial]);
  useEffect(() => {
    if ((observeOnMount && !initial) || open) void refresh().catch(() => {});
  }, [observeOnMount, open, refresh, initial]);
  const quotaWaiting = pdfIRQuotaWaiting(structure);
  const busy = structure?.status === 'queued' || structure?.status === 'running';
  useEffect(() => {
    if (!busy || quotaWaiting && !open) return;
    const timer = setInterval(() => {
      if (!read.current && window.document.visibilityState !== 'hidden') void refresh().catch(() => {});
    }, quotaWaiting ? 30_000 : 2_000);
    return () => clearInterval(timer);
  }, [busy, quotaWaiting, open, refresh]);
  const badgeState = structure ? badgeStates[structure.status] : fallback;
  const submit = async () => {
    if (submitting.current || disabledReason || (busy && !quotaWaiting)) return;
    submitting.current = true;
    read.current?.abort();
    try {
      const force = badgeState !== 'not_requested' && (!quotaWaiting || !!structure?.available);
      const { document: retainedDocument, ...state } = await (onSubmit ? onSubmit(document, force) : requestPdfIR(dataset, document, force));
      if (mounted.current) {
        // The queue acknowledgement may omit the retained document; refresh observes it directly.
        setObserved({ initial, value: { ...state, available: !!retainedDocument || !!structureRef.current?.available } });
      }
    } finally {
      submitting.current = false;
    }
  };
  return <PdfParseStatus status={badgeState} error={structure ? structure.error_message : fallbackError} available={structure?.available}
    quotaWaiting={quotaWaiting} onOpenChange={setOpen} onRefresh={refresh} onParse={submit}
    disabledReason={disabledReason || (structure?.status === 'unavailable' ? structure.error_message || 'PDF 结构不可用' : undefined)} />;
}
