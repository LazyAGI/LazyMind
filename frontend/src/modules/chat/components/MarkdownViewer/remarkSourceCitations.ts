import { parseSourceCitationIds } from "@/modules/chat/utils/sourceAdapter";

interface MarkdownNode {
  type: string;
  children?: MarkdownNode[];
  url?: string;
  value?: string;
}

interface CollectedCitation {
  ids: string[];
  link?: MarkdownNode;
}

const LEADING_PUNCTUATION_PATTERN = /^[。．，,、；;：:!！?？]/;

function sourceIds(node: MarkdownNode): string[] {
  return node.type === "link" ? parseSourceCitationIds(node.url) : [];
}

function cleanTextBoundary(left: MarkdownNode, right: MarkdownNode) {
  if (left.type !== "text" || right.type !== "text") {
    return;
  }
  if (LEADING_PUNCTUATION_PATTERN.test(right.value ?? "")) {
    left.value = (left.value ?? "").replace(/[ \t]+$/, "");
  } else if (/[ \t]$/.test(left.value ?? "") && /^[ \t]/.test(right.value ?? "")) {
    right.value = (right.value ?? "").replace(/^[ \t]+/, "");
  }
}

function collectSourceLinks(node: MarkdownNode, collected: CollectedCitation) {
  if (!node.children || node.type === "code" || node.type === "inlineCode") {
    return;
  }

  const nextChildren: MarkdownNode[] = [];
  for (const child of node.children) {
    const ids = sourceIds(child);
    if (ids.length) {
      collected.link ??= child;
      for (const id of ids) {
        if (!collected.ids.includes(id)) {
          collected.ids.push(id);
        }
      }
      continue;
    }
    collectSourceLinks(child, collected);
    const previous = nextChildren[nextChildren.length - 1];
    if (previous) {
      cleanTextBoundary(previous, child);
    }
    nextChildren.push(child);
  }
  node.children = nextChildren;
}

function appendCollectedLink(node: MarkdownNode, collected: CollectedCitation) {
  if (!collected.link || !collected.ids.length) {
    return;
  }
  node.children ??= [];
  node.children.push({
    ...collected.link,
    url: `#source-${collected.ids.join(",")}`,
  });
}

function linkKnownMarkers(node: MarkdownNode, ids: ReadonlySet<string>) {
  if (!node.children || node.type === "link" || node.type === "linkReference") return;
  node.children = node.children.flatMap(child => {
    if (child.type !== "text" || !child.value) {
      linkKnownMarkers(child, ids);
      return [child];
    }
    const value = child.value;
    const linked: MarkdownNode[] = [];
    let end = 0;
    for (const match of value.matchAll(/\[\[(\d+\.\d+)\]\]|\[(\d+\.\d+)\]/g)) {
      const id = match[1] || match[2];
      if (!ids.has(id)) continue;
      if (match.index > end) linked.push({ type: "text", value: value.slice(end, match.index) });
      linked.push({ type: "link", url: `#source-${id}`, children: [{ type: "text", value: id }] });
      end = match.index + match[0].length;
    }
    if (!linked.length) return [child];
    if (end < value.length) linked.push({ type: "text", value: value.slice(end) });
    return linked;
  });
}

function transformNode(node: MarkdownNode) {
  if (node.type === "paragraph" || node.type === "heading") {
    const collected: CollectedCitation = { ids: [] };
    collectSourceLinks(node, collected);
    appendCollectedLink(node, collected);
    return;
  }

  if (node.type === "tableCell") {
    const collected: CollectedCitation = { ids: [] };
    collectSourceLinks(node, collected);
    appendCollectedLink(node, collected);
    return;
  }

  node.children?.forEach(transformNode);
}

export default function remarkSourceCitations(ids?: ReadonlySet<string>) {
  return (tree: MarkdownNode) => {
    if (ids?.size) linkKnownMarkers(tree, ids);
    transformNode(tree);
  };
}
