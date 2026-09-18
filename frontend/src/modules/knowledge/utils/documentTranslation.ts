import JSZip from "@progress/jszip-esm";

export interface DocumentTranslationUnit { id: string; text: string }
export interface PreparedDocumentTranslation {
  units: DocumentTranslationUnit[];
  build: (translations: Map<string, string>) => Promise<Blob>;
  contentType: string;
}

const mimeTypes: Record<string, string> = {
  docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  pptx: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  xlsx: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  md: "text/markdown;charset=utf-8", markdown: "text/markdown;charset=utf-8",
  txt: "text/plain;charset=utf-8", html: "text/html;charset=utf-8", htm: "text/html;charset=utf-8",
};

function preparePlainText(source: ArrayBuffer, extension: string): PreparedDocumentTranslation {
  const parts = new TextDecoder().decode(source).split(/(\n\s*\n)/);
  const units: DocumentTranslationUnit[] = [];
  const unitByPart = new Map<number, string>();
  parts.forEach((part, index) => {
    if (!part.trim() || /^\s*```[\s\S]*```\s*$/.test(part)) return;
    const id = `text-${index}`;
    units.push({ id, text: part });
    unitByPart.set(index, id);
  });
  return {
    units, contentType: mimeTypes[extension],
    build: async (translations) => new Blob([parts.map((part, index) => translations.get(unitByPart.get(index) || "") ?? part).join("")], { type: mimeTypes[extension] }),
  };
}

function xmlTextGroups(document: XMLDocument, extension: string): Element[][] {
  if (extension === "docx") return Array.from(document.getElementsByTagName("w:p")).map((element) => Array.from(element.getElementsByTagName("w:t")));
  if (extension === "pptx") return Array.from(document.getElementsByTagName("a:p")).map((element) => Array.from(element.getElementsByTagName("a:t")));
  return Array.from(document.getElementsByTagName("si")).map((element) => Array.from(element.getElementsByTagName("t")));
}

async function prepareOpenXml(source: ArrayBuffer, extension: string): Promise<PreparedDocumentTranslation> {
  const zip = await new JSZip().loadAsync(source);
  const pattern = extension === "docx"
    ? /^word\/(document|header\d+|footer\d+|footnotes|endnotes|comments)\.xml$/
    : extension === "pptx" ? /^ppt\/(slides\/slide\d+|notesSlides\/notesSlide\d+)\.xml$/ : /^xl\/sharedStrings\.xml$/;
  const entries: Array<{ path: string; document: XMLDocument; groups: Array<{ id: string; nodes: Element[] }> }> = [];
  const units: DocumentTranslationUnit[] = [];
  for (const path of Object.keys(zip.files).filter((name) => pattern.test(name)).sort()) {
    const xml = await zip.file(path)?.async("string");
    if (!xml) continue;
    const document = new DOMParser().parseFromString(xml, "application/xml");
    const groups = xmlTextGroups(document, extension).map((nodes, index) => ({ id: `${path}-${index}`, nodes })).filter(({ nodes }) => nodes.some((node) => node.textContent?.trim()));
    groups.forEach(({ id, nodes }) => units.push({ id, text: nodes.map((node) => node.textContent || "").join("") }));
    entries.push({ path, document, groups });
  }
  return {
    units, contentType: mimeTypes[extension],
    build: async (translations) => {
      entries.forEach(({ path, document, groups }) => {
        groups.forEach(({ id, nodes }) => {
          const translated = translations.get(id);
          if (translated == null) return;
          nodes.forEach((node, index) => { node.textContent = index === 0 ? translated : ""; });
        });
        zip.file(path, new XMLSerializer().serializeToString(document));
      });
      return zip.generateAsync({ type: "blob", mimeType: mimeTypes[extension], compression: "DEFLATE" });
    },
  };
}

function prepareHtml(source: ArrayBuffer, extension: string): PreparedDocumentTranslation {
  const document = new DOMParser().parseFromString(new TextDecoder().decode(source), "text/html");
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const nodes: Text[] = [];
  const units: DocumentTranslationUnit[] = [];
  let node: Node | null;
  while ((node = walker.nextNode())) {
    const parent = node.parentElement?.tagName.toLowerCase();
    if (!node.textContent?.trim() || parent === "script" || parent === "style") continue;
    const id = `html-${nodes.length}`;
    nodes.push(node as Text); units.push({ id, text: node.textContent });
  }
  return {
    units, contentType: mimeTypes[extension],
    build: async (translations) => {
      nodes.forEach((textNode, index) => { textNode.textContent = translations.get(`html-${index}`) ?? textNode.textContent; });
      return new Blob([`<!doctype html>\n${document.documentElement.outerHTML}`], { type: mimeTypes[extension] });
    },
  };
}

export const translatableDocumentExtensions = ["pdf", "docx", "pptx", "xlsx", "md", "markdown", "txt", "html", "htm"];

export async function prepareDocumentTranslation(source: ArrayBuffer, extension: string): Promise<PreparedDocumentTranslation> {
  const normalized = extension.toLowerCase();
  if (["docx", "pptx", "xlsx"].includes(normalized)) return prepareOpenXml(source, normalized);
  if (["html", "htm"].includes(normalized)) return prepareHtml(source, normalized);
  if (["md", "markdown", "txt"].includes(normalized)) return preparePlainText(source, normalized);
  throw new Error(`暂不支持翻译 .${extension} 文件`);
}
