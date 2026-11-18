import { useCallback, useEffect, useRef, useState } from "react";
import type {
  PaperGuideRevisionResponse,
  PaperGuideStatusResponse,
} from "@/api/generated/core-client";
import { paperGuideApi } from "../../api/paperGuide";
import { usePaperReaderSession } from "../../api/paperReader";
import { isGuideRunActive } from "./guideRunState";

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
    writingRevision?: PaperGuideRevisionResponse;
    error?: unknown;
    loading?: boolean;
  }>({ key });
  const live = useRef<{
    key: string;
    etag?: string;
    selected?: string;
    revisionPending?: boolean;
    revision?: PaperGuideRevisionResponse;
    writingRevision?: PaperGuideRevisionResponse;
    status?: PaperGuideStatusResponse;
    controller?: AbortController;
    schedulePoll?: () => void;
  }>();
  const refresh = useCallback(async () => {
    if (!enabled) return;
    const session = live.current;
    if (!session || session.key !== key) return;
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
        setState({ key, status: session.status, revision: session.revision, writingRevision: session.writingRevision });
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
      const candidate = session.status?.candidate_revision;
      if (candidate && candidate.id !== selected && candidate.run.stage === "writing") {
        const previous = session.writingRevision?.id === candidate.id ? session.writingRevision : undefined;
        if (previous?.version !== candidate.version) {
          const delta = await paperGuideApi.revision(
            { dataset, document, language }, candidate.id, previous?.version ?? 0, controller.signal,
          );
          if (controller.signal.aborted || live.current !== session) return;
          session.writingRevision = mergeRevision(previous, delta);
        }
      } else {
        session.writingRevision = undefined;
      }
      setState({ key, status: session.status, revision: session.revision, writingRevision: session.writingRevision });
    } catch (error) {
      if (!controller.signal.aborted && live.current === session) {
        session.etag = undefined;
        const status = (error as { response?: { status?: number } })?.response
          ?.status;
        if (status === 403 || status === 404) {
          session.status = undefined;
          session.revision = undefined;
          session.writingRevision = undefined;
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
    } finally {
      if (!controller.signal.aborted && session.controller === controller) {
        session.controller = undefined;
        session.schedulePoll?.();
      }
    }
  }, [key, dataset, document, language, enabled]);
  useEffect(() => {
    const session: NonNullable<typeof live.current> = { key };
    live.current = enabled ? session : undefined;
    setState({ key, loading: enabled });
    if (!enabled) return;
    let timer: number | undefined;
    const pollIfVisible = () => {
      if (window.document.visibilityState !== "hidden" && !session.controller) {
        void refresh();
      }
    };
    session.schedulePoll = () => {
      window.clearTimeout(timer);
      const active = isGuideRunActive(session.status?.candidate_revision?.run.stage);
      timer = window.setTimeout(pollIfVisible, active ? 4000 : 60000);
    };
    window.addEventListener("focus", pollIfVisible);
    window.document.addEventListener("visibilitychange", pollIfVisible);
    pollIfVisible();
    return () => {
      window.removeEventListener("focus", pollIfVisible);
      window.document.removeEventListener("visibilitychange", pollIfVisible);
      window.clearTimeout(timer);
      session.controller?.abort();
      live.current = undefined;
    };
  }, [key, refresh, enabled]);
  const selectRevision = useCallback(
    async (id: string) => {
      const session = live.current;
      if (!session || session.key !== key) return;
      session.selected = id;
      session.revision = undefined;
      await refresh();
    },
    [key, refresh],
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
