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

export default function remarkSourceCitations() {
  return (tree: MarkdownNode) => transformNode(tree);
}
