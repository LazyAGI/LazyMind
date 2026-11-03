import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ArtifactPreviewTarget } from './target';
import { message } from 'antd';
import { useTranslation } from 'react-i18next';
import { useWorkflowStore } from '../../store/workflowPanel';

export interface PreviewReturn {
  onClose?: () => void;
  onVersions?: () => void;
}

export function useArtifactPreviewController(conversationId: string) {
  const { t } = useTranslation();
  const [selection, setSelection] = useState<{ target: ArtifactPreviewTarget; conversationId: string; actions?: PreviewReturn } | null>(null);
  const restore = useRef<HTMLElement | null>(null);
  const current = useRef(selection);
  current.current = selection;
  const target = selection?.conversationId === conversationId ? selection.target : null;

  useEffect(() => { setSelection(null); restore.current = null; }, [conversationId]);
  const open = useCallback((next: ArtifactPreviewTarget, actions?: PreviewReturn) => {
    if (next.conversationId && next.conversationId !== conversationId) return;
    if (next.workflow && (!next.workflow.stepId || next.workflow.sessionId !== useWorkflowStore.getState().sessionByConversation[conversationId]?.session_id)) {
      message.info(t('chat.preview.workflowUnavailable'));
      return;
    }
    if (current.current?.target.key === next.key) {
      // Reopening from the file list must still restore that list on close.
      if (actions) {
        restore.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        setSelection(previous => previous && { ...previous, actions });
      }
      return;
    }
    restore.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setSelection({ target: next, conversationId, actions });
  }, [conversationId, t]);
  const close = useCallback(() => {
    const previous = current.current;
    setSelection(null);
    if (previous?.conversationId !== conversationId) return;
    previous.actions?.onClose?.();
    const element = restore.current;
    requestAnimationFrame(() => { if (element?.isConnected) element.focus(); });
  }, [conversationId]);
  return useMemo(() => ({ target, open, close, onVersions: selection?.actions?.onVersions }), [target, open, close, selection?.actions?.onVersions]);
}

export const ArtifactPreviewContext = createContext<ReturnType<typeof useArtifactPreviewController> | null>(null);
export const useArtifactPreview = () => useContext(ArtifactPreviewContext);
