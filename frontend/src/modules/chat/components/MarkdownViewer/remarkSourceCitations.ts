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

const SOURCE_URL_PATTERN = /^#(?:user-content-)?source-(.+)$/;
const LEADING_PUNCTUATION_PATTERN = /^[。．，,、；;：:!！?？]/;

function sourceIds(node: MarkdownNode): string[] {
  if (node.type !== "link" || typeof node.url !== "string") {
    return [];
  }
  const match = SOURCE_URL_PATTERN.exec(node.url);
  return match
    ? match[1].split(",").map((id) => id.trim()).filter(Boolean)
    : [];
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

function transformNode(node: MarkdownNode) {
  if (node.type === "paragraph" || node.type === "heading") {
    const collected: CollectedCitation = { ids: [] };
    collectSourceLinks(node, collected);
    appendCollectedLink(node, collected);
    return;
  }

  if (node.type === "tableRow") {
    const collected: CollectedCitation = { ids: [] };
    collectSourceLinks(node, collected);
    const lastCell = node.children?.[node.children.length - 1];
    if (lastCell) {
      appendCollectedLink(lastCell, collected);
    }
    return;
  }

  node.children?.forEach(transformNode);
}

export default function remarkSourceCitations() {
  return (tree: MarkdownNode) => transformNode(tree);
}
