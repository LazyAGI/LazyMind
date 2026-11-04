import type { PageProps } from "react-pdf";

/** Keep PDF.js's own clickable links while revealing source-colored link boxes. */
export const restorePdfLinkAnnotationBorders: NonNullable<PageProps["filterAnnotations"]> = ({ annotations }) =>
  annotations.map(annotation => {
    if (annotation?.subtype !== "Link" || annotation?.borderStyle?.width !== 0) return annotation;
    const color = annotation.borderColor as ArrayLike<number> | undefined;
    if (!color || color.length !== 3) return annotation;
    const channels = [color[0], color[1], color[2]];
    if (!channels.every(Number.isFinite) || Math.max(...channels) - Math.min(...channels) < 40) return annotation;
    // Some PDFs retain the link color in /C but explicitly set /Border width
    // to zero. Give those colored links a thin PDF.js-rendered border; leave
    // the original annotation and its destination untouched.
    return { ...annotation, borderStyle: { ...annotation.borderStyle, width: 1 } };
  });
