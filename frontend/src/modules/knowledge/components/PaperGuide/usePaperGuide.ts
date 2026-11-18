import { useCallback, useEffect, useRef, useState } from "react";
import type {
  PaperGuideRevisionResponse,
  PaperGuideStatusResponse,
} from "@/api/generated/core-client";
import { paperGuideApi } from "../../api/paperGuide";
import { usePaperReaderSession } from "../../api/paperReader";
export function unicodeRange(text: string, start: number, end: number) {
  return {
    start: Array.from(text.slice(0, start)).length,
    end: Array.from(text.slice(0, end)).length,
  };
}
export function mergeRevision(
  previous: PaperGuideRevisionResponse | undefined,
  next: PaperGuideRevisionResponse,
): PaperGuideRevisionResponse {
  if (!previous || previous.id !== next.id)
    return { ...next, sections: next.sections || [] };
  const sections = [...previous.sections];
  for (const section of next.sections || []) {
    const index = sections.findIndex((s) => s.role === section.role);
    if (index < 0) sections.push(section);
    else if (section.change_version >= sections[index].change_version)
      sections[index] = section;
  }
  return { ...next, sections };
}
export function usePaperGuide(
  dataset: string,
  document: string,
  language: string,
  enabled: boolean,
) {
  const identity = usePaperReaderSession();
  const key = `${identity}/${dataset}/${document}/${language}`;
  const [state, setState] = useState<{
    key: string;
    status?: PaperGuideStatusResponse;
    revision?: PaperGuideRevisionResponse;
    error?: unknown;
    loading?: boolean;
  }>({ key });
  const live = useRef<{
    key: string;
    etag?: string;
    selected?: string;
    revisionPending?: boolean;
    revision?: PaperGuideRevisionResponse;
    status?: PaperGuideStatusResponse;
    controller?: AbortController;
  }>({ key });
  const refresh = useCallback(async () => {
    if (!enabled) return;
    const session = live.current;
    if (session.key !== key) return;
    session.controller?.abort();
    const controller = new AbortController();
    session.controller = controller;
    try {
      const response = await paperGuideApi.status(
        { dataset, document, language },
        { signal: controller.signal, etag: session.etag },
      );
      if (controller.signal.aborted || live.current !== session) return;
      if (!response.data && session.revision && !session.revisionPending) {
        setState({ key, status: session.status, revision: session.revision });
        return;
      }
      const followLatest =
        !session.revision?.sections.some(section => section.blocks.length > 0) &&
        !session.status?.published_revision;
      session.etag = response.etag || session.etag;
      session.status = response.data || session.status;
      const newest =
        session.status?.published_revision?.id ||
        session.status?.candidate_revision?.id;
      // An empty, unpublished failed attempt is not a reading position to
      // preserve. Follow its replacement without disturbing any readable draft.
      const selected = followLatest
        ? newest || session.selected
        : session.selected || newest;
      if (selected) {
        session.selected = selected;
        session.revisionPending = true;
        const delta = await paperGuideApi.revision(
          { dataset, document, language },
          selected,
          session.revision?.id === selected ? session.revision.version : 0,
          controller.signal,
        );
        if (controller.signal.aborted || live.current !== session) return;
        session.revision = mergeRevision(session.revision, delta);
        session.revisionPending = false;
      }
      setState({ key, status: session.status, revision: session.revision });
    } catch (error) {
      if (!controller.signal.aborted && live.current === session) {
        const status = (error as { response?: { status?: number } })?.response
          ?.status;
        if (status === 403 || status === 404) {
          session.status = undefined;
          session.revision = undefined;
          session.etag = undefined;
          session.selected = undefined;
          session.revisionPending = false;
        }
        setState({
          key,
          status: session.status,
          revision: session.revision,
          error,
        });
      }
    }
  }, [key, dataset, document, language, enabled]);
  useEffect(() => {
    const session = { key };
    live.current = session;
    setState({ key, loading: enabled });
    void refresh();
    const timer = enabled
      ? window.setInterval(() => void refresh(), 4000)
      : undefined;
    return () => {
      window.clearInterval(timer);
      live.current.controller?.abort();
    };
  }, [key, refresh, enabled]);
  const selectRevision = useCallback(
    async (id: string) => {
      live.current.selected = id;
      live.current.revision = undefined;
      await refresh();
    },
    [refresh],
  );
  const current = state.key === key ? state : { key, loading: enabled };
  const available =
    current.status?.published_revision?.id !== current.revision?.id
      ? current.status?.published_revision
      : undefined;
  return {
    ...current,
    refresh,
    selectRevision,
    availableRevision: available,
    identity,
  };
}
