import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { axiosInstance, BASE_URL } from "@/components/request";
import { getDocumentParseStatus } from "@/modules/knowledge/api/pdfArtifacts";

export interface ParsedPreviewTarget { dataset: string; document: string }
interface SegmentPage {
  segments?: Array<{ content?: string; display_content?: string; text?: string }>;
  next_page_token?: string;
}

// Read existing materialized blocks only. Notebook code and rich outputs are
// displayed as React text, never interpreted as HTML or executed.
export default function ParsedDocumentPreview({ dataset, document }: ParsedPreviewTarget) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<"loading" | "ready" | "unavailable" | "error">("loading");
  const [blocks, setBlocks] = useState<string[]>([]);
  const [nextPage, setNextPage] = useState("");
  const [loadingMore, setLoadingMore] = useState(false);
  const [pageError, setPageError] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const currentRequest = useRef<AbortController>();

  const readPage = async (controller: AbortController, token = "") => {
    const response = await axiosInstance.get<SegmentPage | { data: SegmentPage }>(
      `${BASE_URL}/api/core/datasets/${encodeURIComponent(dataset)}/documents/${encodeURIComponent(document)}/segments`,
      { params: { group: "block", page_size: 100, page_token: token || undefined }, signal: controller.signal, silentError: true } as never,
    );
    if (controller.signal.aborted) return;
    const page = "data" in response.data ? response.data.data : response.data;
    const text = (page.segments || []).map(block => block.display_content || block.content || block.text || "").filter(value => value.trim());
    setBlocks(previous => token ? [...previous, ...text] : text);
    setNextPage(page.next_page_token || "");
    setStatus("ready");
  };

  useEffect(() => {
    const controller = new AbortController();
    currentRequest.current = controller;
    setStatus("loading");
    setBlocks([]);
    setNextPage("");
    setLoadingMore(false);
    setPageError(false);
    void (async () => {
      try {
        const result = await getDocumentParseStatus(dataset, document);
        if (controller.signal.aborted) return;
        if (result.status !== "parsed") { setStatus("unavailable"); return; }
        await readPage(controller);
      } catch {
        if (!controller.signal.aborted) setStatus("error");
      }
    })();
    return () => controller.abort();
    // readPage uses only the document identity and stable state setters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataset, document, attempt]);

  const loadMore = async () => {
    const controller = currentRequest.current;
    if (!controller || controller.signal.aborted || loadingMore || !nextPage) return;
    setLoadingMore(true);
    setPageError(false);
    try { await readPage(controller, nextPage); }
    catch { if (!controller.signal.aborted) setPageError(true); }
    finally { if (!controller.signal.aborted) setLoadingMore(false); }
  };

  return <section className="file-viewer-parsed-preview" aria-label={t("knowledge.parsedPreviewTitle")}>
    <p>{t("knowledge.parsedPreviewHint")}</p>
    {status === "loading" && <p role="status">{t("knowledge.dataLoading")}</p>}
    {status === "unavailable" && <p role="status">{t("knowledge.parsedPreviewUnavailable")}</p>}
    {status === "error" && <p role="alert">{t("knowledge.parsedPreviewFailed")}</p>}
    {(status === "unavailable" || status === "error") && <button type="button" onClick={() => setAttempt(value => value + 1)}>{t("knowledge.pdfChatRefreshStatus")}</button>}
    {status === "ready" && !blocks.length && <p role="status">{t("knowledge.parsedPreviewEmpty")}</p>}
    {blocks.map((text, index) => <pre key={index}>{text}</pre>)}
    {pageError && <p role="alert">{t("knowledge.parsedPreviewFailed")}</p>}
    {nextPage && <button type="button" disabled={loadingMore} onClick={() => void loadMore()}>{t("knowledge.parsedPreviewMore")}</button>}
  </section>;
}
