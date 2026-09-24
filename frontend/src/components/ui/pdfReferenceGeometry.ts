export interface PdfReferenceAction {
  referenceId: string;
  referenceKey?: string;
  rawText: string;
  kind: "download" | "open" | "external";
  href?: string;
  page?: number;
  bbox?: [number, number, number, number];
}

export function referenceActionAtPoint(actions: PdfReferenceAction[], page: number, x: number, y: number) {
  return actions.find((action) => action.page === page && action.bbox
    && x >= action.bbox[0] && x <= action.bbox[2]
    && y >= action.bbox[1] && y <= action.bbox[3]);
}

export function referenceActionForSelection(actions: PdfReferenceAction[], page: number, bbox?: [number, number, number, number]) {
  if (!bbox) return undefined;
  return actions.find((action) => action.page === page && action.bbox
    && Math.max(bbox[0], action.bbox[0]) <= Math.min(bbox[2], action.bbox[2])
    && Math.max(bbox[1], action.bbox[1]) <= Math.min(bbox[3], action.bbox[3]));
}
