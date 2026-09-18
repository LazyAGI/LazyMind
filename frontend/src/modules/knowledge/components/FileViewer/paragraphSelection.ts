import type { PdfTextSelection } from "@/components/ui";

const normalizedSelectionText = (value: string) => value.replace(/\s+/gu, "").trim();

export const paragraphSelectionsOverlap = (left: PdfTextSelection, right: PdfTextSelection) => {
  if (left.page !== right.page) return false;
  if (left.bbox && right.bbox) {
    const [leftX1, leftY1, leftX2, leftY2] = left.bbox;
    const [rightX1, rightY1, rightX2, rightY2] = right.bbox;
    return leftX1 < rightX2 && leftX2 > rightX1 && leftY1 < rightY2 && leftY2 > rightY1;
  }
  const leftText = normalizedSelectionText(left.text);
  const rightText = normalizedSelectionText(right.text);
  return Boolean(leftText && rightText && (leftText.includes(rightText) || rightText.includes(leftText)));
};
