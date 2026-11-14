import { useCallback, useEffect, useMemo, useRef } from "react";
import { createPdfCitationPreviewCache } from "@/components/ui/pdfCitationPreview";
import type { GuidePreview } from "./PaperGuideAsset";
export function useGuideSourcePreview(
  scopeKey: string,
  revisionHash: string | undefined,
  sourceCurrent: boolean,
  currentHash: string | undefined,
  getPdfData: () => ArrayBuffer | null,
): GuidePreview {
  const cache = useMemo(
    () => createPdfCitationPreviewCache(),
    [scopeKey, revisionHash],
  );
  const pdf = useRef<
    Promise<
      import("@/components/ui/pdfCitationPreview").PdfCitationPreviewDocument & {
        destroy: () => Promise<void>;
      }
    >
  >();
  useEffect(() => {
    pdf.current = undefined;
    return () => {
      cache.dispose();
      const old = pdf.current;
      void old?.then((d) => d.destroy()).catch(() => {});
    };
  }, [cache]);
  const preview: GuidePreview = useCallback(
    async (e) => {
      if (!revisionHash || !sourceCurrent || revisionHash !== currentHash)
        return;
      if (!pdf.current) {
        const data = getPdfData();
        if (!data) return;
        pdf.current = import("react-pdf").then(
          ({ pdfjs }) => pdfjs.getDocument({ data }).promise,
        );
      }
      const doc = await pdf.current;
      if (!doc) return;
      const page = await doc.getPage(e.locator.page + 1);
      const v = page.getViewport({ scale: 1, rotation: 0 });
      const b = e.locator.bbox;
      const bbox: [number, number, number, number] =
        b?.length === 4 && b.every(Number.isFinite)
          ? [b[0] * v.width, b[1] * v.height, b[2] * v.width, b[3] * v.height]
          : [0, 0, v.width, v.height];
      const t = {
        referenceId: e.id,
        referenceKey: e.id,
        rawText: e.text,
        kind: "open" as const,
        regions: [{ page: e.locator.page + 1, bbox }],
      };
      try {
        const crop = await cache.get(doc, t);
        if (crop) return crop;
      } catch {
        /* A damaged crop must not hide the original page. */
      }
      return cache.get(doc, {
        ...t,
        regions: [
          { page: e.locator.page + 1, bbox: [0, 0, v.width, v.height] },
        ],
      });
    },
    [cache, sourceCurrent, revisionHash, currentHash, getPdfData],
  );
  return preview;
}
