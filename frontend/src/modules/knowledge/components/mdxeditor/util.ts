import { useEffect } from "react";

export function useAutoImageFigure() {
  useEffect(() => {
    const root = document.querySelector(".mdx-editor-root") || document;

    const imgs = root.querySelectorAll("img:not([data-has-figure])");

    imgs.forEach((img) => {
      const alt = img.getAttribute("alt") || "";
      const parent = img.parentElement;
      if (!parent) {
        return;
      }

      const figure = document.createElement("figure");
      figure.className = "mdx-figure";

      const caption = document.createElement("figcaption");
      caption.innerText = alt;

      img.setAttribute("data-has-figure", "true");

      parent.insertBefore(figure, img);
      parent.removeChild(img);

      figure.appendChild(img);
      figure.appendChild(caption);
    });
  });
}

// function formatDiffArray(arr: DiffEntry[]): string {
//   if (!Array.isArray(arr)) {
//     return '';
//   }
//   return arr.reduce((acc, entry) => {
//     if (entry.added && !entry.removed) {
//       if (
//         entry.value.indexOf('https://maas-minio.sensecore.dev/') > -1 ||
//         entry.value.indexOf('?X-Amz-Algorithm=') > -1
//       ) {
//         return acc + '';
//       }
//       return acc + entry.value;
//     }
//     if (!entry.added && !entry.removed) {
//       return acc + entry.value;
//     }
//     return acc;
//   }, '');
// }

// export function markCharsFn(str1: string, str2: string): string {
//   const diffRes = diffChars(str1, str2) as unknown as DiffEntry[];
//   return formatDiffArray(diffRes);
// }

export { collapseImagesToKeys as replaceImagesWithKeys } from '@/modules/knowledge/utils/imageUrl';
