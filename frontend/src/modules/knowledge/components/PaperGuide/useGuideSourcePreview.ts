import { useCallback, useEffect, useMemo, useRef } from "react";
import {
  createPdfCitationPreviewCache,
  type PdfCitationPreviewDocument,
} from "@/components/ui/pdfCitationPreview";
import { computeBufferSha256 } from "../../utils/fileHash";
import type { GuidePreview } from "./PaperGuideAsset";

type GuidePdf = PdfCitationPreviewDocument & { destroy: () => Promise<void> };

export function useGuideSourcePreview(
  scopeKey: string,
  revisionHash: string | undefined,
  sourceCurrent: boolean,
  currentHash: string | undefined,
  getPdfData: () => ArrayBuffer | null,
): GuidePreview {
  const scope = useMemo(
    () => ({ scopeKey, revisionHash, sourceCurrent, currentHash, getPdfData }),
    [scopeKey, revisionHash, sourceCurrent, currentHash, getPdfData],
  );
  const active = useRef<{
    scope: typeof scope;
    cache: ReturnType<typeof createPdfCitationPreviewCache>;
    pdf?: Promise<GuidePdf | undefined>;
  }>();
  useEffect(() => {
    const session: NonNullable<typeof active.current> = {
      scope,
      cache: createPdfCitationPreviewCache(),
    };
    active.current = session;
    return () => {
      active.current = undefined;
      session.cache.dispose();
      void session.pdf?.then((d) => d?.destroy()).catch(() => {});
    };
  }, [scope]);

  return useCallback<GuidePreview>(
    async (e, original) => {
      // Children can mount before their parent's passive effects have run.
      await Promise.resolve();
      const session = active.current;
      if (!session || session.scope !== scope || !revisionHash ||
          !sourceCurrent || revisionHash !== currentHash) return;
      if (original && (original.page !== e.locator.page ||
          (original.kind === "formula" ? e.source_type !== "formula" :
           original.kind !== "figure" || !["image", "chart"].includes(e.source_type)))) return;

      if (!session.pdf) {
        const data = getPdfData();
        if (!data) return;
        session.pdf = (async () => {
          const hash = await computeBufferSha256(data);
          if (hash !== revisionHash || active.current !== session) return;
          const { pdfjs } = await import("react-pdf");
          if (active.current !== session) return;
          return pdfjs.getDocument({ data }).promise;
        })();
      }
      const doc = await session.pdf;
      if (!doc || active.current !== session) return;
      const pageNumber = (original?.page ?? e.locator.page) + 1;
      const page = await doc.getPage(pageNumber);
      if (active.current !== session) return;
      const viewport = page.getViewport({ scale: 1, rotation: 0 });
      const { width, height } = viewport;
      const b = original ? original.bbox : e.locator.bbox;
      const target = { referenceId: e.id, referenceKey: e.id, rawText: e.text, kind: "open" as const };
      if (b?.length === 4 && b.every(value => Number.isFinite(value) && value >= 0 && value <= 1) &&
          b[0] < b[2] && b[1] < b[3] && !(b[0] === 0 && b[1] === 0 && b[2] === 1 && b[3] === 1)) {
        try {
          const crop = await session.cache.get(doc, {
            ...target, regions: [{ page: pageNumber, bbox: [b[0] * width, b[1] * height, b[2] * width, b[3] * height] }],
          }, { exact: !!original });
          if (active.current !== session) return;
          if (crop) return { ...crop, mode: "region" };
        } catch {
          // Keep the original page available if its individual crop fails.
          if (active.current !== session) return;
        }
      }
      const pagePreview = await session.cache.get(doc, {
        ...target, regions: [{ page: pageNumber, bbox: [0, 0, width, height] }],
      });
      if (pagePreview && active.current === session) return { ...pagePreview, mode: "page" };
    },
    [scope, revisionHash, sourceCurrent, currentHash, getPdfData],
  );
}
