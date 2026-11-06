import type { PageProps } from "react-pdf";

/** Keep PDF.js's clickable links and its annotation geometry for source link boxes. */
export const restorePdfLinkAnnotationBorders: NonNullable<PageProps["filterAnnotations"]> = ({ annotations }) =>
  annotations.map(annotation => {
    // An appearance stream is already painted on the PDF canvas. Adding an
    // annotation-layer border on top of it produces a second, offset box.
    if (annotation?.subtype !== "Link" || !annotation.borderStyle || annotation.hasAppearance || !annotation.dest) return annotation;
    const color = annotation.borderColor as ArrayLike<number> | undefined;
    if (!color || color.length !== 3) return annotation;
    const [red, green, blue] = [color[0], color[1], color[2]];
    if (![red, green, blue].every(Number.isFinite)) return annotation;
    const greenLink = green - red >= 40 && green - blue >= 40;
    const redLink = red - green >= 40 && red - blue >= 40;
    if (!greenLink && !redLink) return annotation;
    // Some papers encode red/green link categories in /C while setting a
    // zero-width /Border. PDF.js still owns positioning and the clickable
    // element; only the missing outline is supplied here. Existing borders
    // keep their source width. Tone down primary colors consistently across
    // both cases so the frame resembles a reader annotation rather than neon ink.
    const saturated = Math.max(red, green, blue) >= 220 && Math.min(red, green, blue) <= 35;
    const borderColor = saturated
      ? new Uint8ClampedArray(greenLink ? [54, 154, 78] : [200, 68, 68])
      : annotation.borderColor;
    const borderStyle = annotation.borderStyle.width === 0
      ? { ...annotation.borderStyle, width: 1 }
      : annotation.borderStyle;
    return borderColor === annotation.borderColor && borderStyle === annotation.borderStyle
      ? annotation
      : { ...annotation, borderColor, borderStyle };
  });
