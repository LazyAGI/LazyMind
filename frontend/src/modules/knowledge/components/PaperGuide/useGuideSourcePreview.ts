import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  createPdfCitationPreviewCache,
  type PdfCitationPreviewDocument,
} from "@/components/ui/pdfCitationPreview";
import { pdfDocumentOptions } from "@/components/ui/pdfDocumentOptions";
import { computeBufferSha256 } from "../../utils/fileHash";
import type { GuidePreview } from "./PaperGuideAsset";

type GuidePdf = PdfCitationPreviewDocument & { destroy: () => Promise<void> };
type VerifiedBytes = { version: object | null | undefined; verified: Promise<ArrayBuffer | undefined> };

export function useGuideSourcePreview(
  scopeKey: string,
  revisionHash: string | undefined,
  sourceCurrent: boolean,
  currentHash: string | undefined,
  getPdfData: () => ArrayBuffer | null | Promise<ArrayBuffer>,
  getPdfVersion?: () => object | null,
) {
  const versionAtRender = getPdfVersion?.();
  const scope = useMemo(
    () => ({ scopeKey, revisionHash, sourceCurrent, currentHash, getPdfData, getPdfVersion, versionAtRender }),
    [scopeKey, revisionHash, sourceCurrent, currentHash, getPdfData, getPdfVersion, versionAtRender],
  );
  const active = useRef<{
    scope: typeof scope;
    cache: ReturnType<typeof createPdfCitationPreviewCache>;
    bytes?: VerifiedBytes;
    pdf?: { bytes: VerifiedBytes; document: Promise<GuidePdf | undefined> };
  }>();
  const [verified, setVerified] = useState<{ scope: typeof scope; version: object | null | undefined }>();
  useEffect(() => {
    const session: NonNullable<typeof active.current> = {
      scope,
      cache: createPdfCitationPreviewCache({ renderScale: 3 }),
    };
    active.current = session;
    return () => {
      active.current = undefined;
      session.cache.dispose();
      void session.pdf?.document.then((d) => d?.destroy()).catch(() => {});
    };
  }, [scope]);

  const verifiedBytes = useCallback(async (fresh = false) => {
    await Promise.resolve();
    const session = active.current;
    if (!session || session.scope !== scope || !revisionHash ||
        !sourceCurrent || revisionHash !== currentHash) return;
    const version = getPdfVersion?.();
    if (version === null) return;
    if (!session.bytes || session.bytes.version !== version || (fresh && !getPdfVersion)) {
      const data = getPdfData();
      if (!data) return;
      session.bytes = { version, verified: Promise.resolve(data).then(async bytes =>
        (await computeBufferSha256(bytes)) === revisionHash ? bytes : undefined) };
    }
    const result = session.bytes;
    const data = await result.verified;
    if (active.current !== session || session.bytes !== result || getPdfVersion?.() !== version) return;
    if (data) setVerified(previous => previous?.scope === scope && previous.version === version ? previous : { scope, version });
    else setVerified(previous => previous?.scope === scope ? undefined : previous);
    return data ? { session, result, data } : undefined;
  }, [scope, revisionHash, sourceCurrent, currentHash, getPdfData, getPdfVersion]);

  const verifyCurrentSource = useCallback(async () => {
    const verified = await verifiedBytes(true);
    return verified && (() => active.current === verified.session && getPdfVersion?.() === verified.result.version);
  }, [verifiedBytes, getPdfVersion]);
  const preview = useCallback<GuidePreview>(
    async (e, original) => {
      if (original && (original.page !== e.locator.page ||
          (original.kind === "formula" ? e.source_type !== "formula" :
           original.kind !== "figure" || !["image", "chart"].includes(e.source_type)))) return;

      const bytes = await verifiedBytes();
      if (!bytes) return;
      const { session, result, data } = bytes;
      const isCurrent = () => active.current === session && getPdfVersion?.() === result.version;
      if (!session.pdf || session.pdf.bytes !== result) {
        const old = session.pdf;
        void old?.document.then(doc => {
          if (doc) session.cache.disposeDocument(doc);
          return doc?.destroy();
        }).catch(() => {});
        session.pdf = { bytes: result, document: (async () => {
          const { pdfjs } = await import("react-pdf");
          if (!isCurrent()) return;
          return pdfjs.getDocument({ ...pdfDocumentOptions, data }).promise;
        })() };
      }
      const doc = await session.pdf.document;
      if (!doc || !isCurrent()) return;
      const pageNumber = (original?.page ?? e.locator.page) + 1;
      const page = await doc.getPage(pageNumber);
      if (!isCurrent()) return;
      const viewport = page.getViewport({ scale: 1, rotation: 0 });
      const { width, height } = viewport;
      // A citation may identify a caption, a paragraph, or the full figure group.
      // Only a verified original display identifies the pixels of an image.
      const figure = ["image", "chart"].includes(e.source_type);
      const b = original ? original.bbox : figure ? undefined : e.locator.bbox;
      const target = { referenceId: e.id, referenceKey: e.id, rawText: e.text, kind: "open" as const };
      if ((!figure || !(page.rotate % 360)) && b?.length === 4 && b.every(value => Number.isFinite(value) && value >= 0 && value <= 1) &&
          b[0] < b[2] && b[1] < b[3] && !(b[0] === 0 && b[1] === 0 && b[2] === 1 && b[3] === 1)) {
        try {
          const crop = await session.cache.get(doc, {
            ...target, regions: [{ page: pageNumber, bbox: [b[0] * width, b[1] * height, b[2] * width, b[3] * height] }],
          }, { exact: !!original, trimWhitespace: original?.kind === "figure"
            ? (b[0] === 0 && b[1] === 0 && b[2] === 1 ? "all" : "horizontal") : undefined });
          if (!isCurrent()) return;
          if (crop) return { ...crop, mode: "region" };
        } catch {
          // Keep the original page available if its individual crop fails.
          if (!isCurrent()) return;
        }
      }
      const pagePreview = await session.cache.get(doc, {
        ...target, regions: [{ page: pageNumber, bbox: [0, 0, width, height] }],
      });
      if (pagePreview && isCurrent()) return { ...pagePreview, mode: "page" };
    },
    [verifiedBytes, getPdfVersion],
  );
  return {
    preview,
    verifyCurrentSource,
    sourceVerified: !!verified && verified.scope === scope && verified.version === versionAtRender,
  };
}
