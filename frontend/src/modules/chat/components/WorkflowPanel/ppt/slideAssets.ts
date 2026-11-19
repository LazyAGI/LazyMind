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

/** Raster captures carry font bytes; native PPTX export resolves the shared font IDs. */
export async function embedSlideAssetsForExport(
  html: string, { embedFonts = true }: { embedFonts?: boolean } = {},
): Promise<string> {
  const pattern = /(?<=["'(\s])(?:\/api\/core)?\/static-files\/[^\s"'<>\)]+/g;
  const paths = [...new Set(html.match(pattern) || [])];
  const embedded = new Map(await Promise.all(paths.map(async path => {
    const font = path.match(/^(?:\/api\/core)?\/static-files\/presentation-fonts\/[a-f0-9]{64}\.(ttf|otf)(?:\?|$)/);
    if (font && !embedFonts) return [path, path] as const;
    const url = await resolveMarkdownImageUrlAsync(path.replaceAll('&amp;', '&'));
    const response = await fetch(url);
    if (!response.ok) throw new Error(`Slide ${font ? 'font' : 'image'} download failed (${response.status})`);
    let blob = await response.blob();
    if (font) {
      if (blob.type && !/^(font\/|application\/(?:octet-stream|x-font|font))/.test(blob.type)) {
        throw new Error('Slide asset is not a font');
      }
      blob = new Blob([blob], { type: `font/${font[1]}` });
    } else if (!blob.type.startsWith('image/')) throw new Error('Slide asset is not an image');
    const data = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(new Error(`Slide ${font ? 'font' : 'image'} could not be read`));
      reader.readAsDataURL(blob);
    });
    return [path, data] as const;
  })));
  return html.replace(pattern, path => embedded.get(path) || path);
}
