interface PdfAnchorScrollOffsets {
  liveTop: number;
  recordedTop: number;
  previousMaxScrollTop: number;
  maxScrollTop: number;
}

/**
 * Choose an offset in the previous page geometry before calculating the new
 * page-relative anchor. Usually the live DOM includes newer wheel movement;
 * after shrinking the content, it can instead already contain a browser clamp.
 * The recorded offset must track scroll events and explicit navigation.
 */
export function resolvePdfAnchorScrollTop({
  liveTop,
  recordedTop,
  previousMaxScrollTop,
  maxScrollTop,
}: PdfAnchorScrollOffsets): number {
  // scrollHeight is rounded while page dimensions and scrollTop can be fractional.
  const tolerance = 1;
  const clampedByLayout = previousMaxScrollTop > maxScrollTop + tolerance
    && recordedTop > maxScrollTop + tolerance
    && Math.abs(liveTop - maxScrollTop) <= tolerance;
  return clampedByLayout ? recordedTop : liveTop;
}
