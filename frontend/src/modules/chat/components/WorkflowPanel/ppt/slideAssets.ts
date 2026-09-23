import { resolveMarkdownImageUrlAsync } from '@/modules/knowledge/utils/imageUrl';

/** Resolve only app-owned asset references, including CSS backgrounds. */
export async function resolveSlideAssets(html: string): Promise<string> {
  const pattern = /(?<=["'(\s])(?:\/api\/core)?\/static-files\/[^\s"'<>\)]+/g;
  const paths = [...new Set(html.match(pattern) || [])];
  const resolved = new Map(await Promise.all(paths.map(async path => [
    path, await resolveMarkdownImageUrlAsync(path.replaceAll('&amp;', '&')),
  ] as const)));
  return html.replace(pattern, path => resolved.get(path) || path);
}

/** Package local images for raster capture or export; ordinary previews keep URLs. */
export async function embedSlideAssetsForExport(html: string): Promise<string> {
  const pattern = /(?<=["'(\s])(?:\/api\/core)?\/static-files\/[^\s"'<>\)]+/g;
  const paths = [...new Set(html.match(pattern) || [])];
  const embedded = new Map(await Promise.all(paths.map(async path => {
    const url = await resolveMarkdownImageUrlAsync(path.replaceAll('&amp;', '&'));
    const response = await fetch(url);
    if (!response.ok) throw new Error(`Slide image download failed (${response.status})`);
    const blob = await response.blob();
    if (!blob.type.startsWith('image/')) throw new Error('Slide asset is not an image');
    const data = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(new Error('Slide image could not be read'));
      reader.readAsDataURL(blob);
    });
    return [path, data] as const;
  })));
  return html.replace(pattern, path => embedded.get(path) || path);
}
