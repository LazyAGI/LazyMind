import Markdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import classnames from "classnames";
import "katex/dist/katex.min.css";
import { Dropdown, Image, Popover, message } from "antd";
import type { MenuProps } from "antd";
import rehypeSanitize from "rehype-sanitize";
import { useTranslation } from "react-i18next";
import "../../../../components/MarkdownViewer/markdown.scss";
import "./index.scss";
import {
  createContext,
  isValidElement,
  memo,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type MouseEvent,
  type ReactNode,
} from "react";
import { customSchema } from "./config";
import rehypeRaw from "rehype-raw";
import {
  basenameFromPath,
  resolveCoreAssetUrl,
  resolveMarkdownImageUrlAsync,
} from "@/modules/knowledge/utils/imageUrl";
import {
  useTaskCenterStore,
  type ConversationArtifact,
} from "@/modules/chat/store/taskCenter";
import {
  conversationHasFileIdLink,
  findArtifactByFileId,
  getArtifactFilename,
  getArtifactSignSource,
  getArtifactTextContent,
  getFileIdFromHref,
  isBrowserDownloadHref,
  isInlineDownloadableArtifact,
  normalizeArtifactFileLinks,
} from "@/modules/chat/utils/artifactLinks";
import {
  downloadArtifactFile,
  isAppleDesktopPlatform,
  revealArtifactFile,
  saveArtifactFileAs,
} from "@/modules/chat/utils/artifactFileActions";
import { hasDesktopFileBridge } from "@/runtime/desktopBridge";
import HtmlBlock from "./HtmlBlock";
import MermaidBlock from "./MermaidBlock";
import EditableBlock from "./EditableBlock";
import {
  getLanguageFromClassName,
  getRawLanguageFromClassName,
  highlightCode,
} from "./syntaxHighlight";
import {
  type ChatSource,
  findSourceByCitationId,
  getSourceEvidenceText,
  getSourceFaviconUrl,
  getSourceHref,
  getSourceLabel,
  getSourceSubtitle,
  isExternalSource,
  normalizeSourceMarkers,
  stripRedundantSourceUrls,
} from "@/modules/chat/utils/sourceAdapter";

const SOURCE_PREFIXES = ["#source-", "#user-content-source-"];
const EMPTY_CONVERSATION_ARTIFACTS: ConversationArtifact[] = [];
const BOLD_BARE_URL_PATTERN = /\*\*((?:https?:\/\/|www\.)[^\s*<>()]+)\*\*/g;
// Matches bare URLs that are NOT already inside Markdown link syntax [...](...)
// Captures trailing fullwidth/CJK punctuation so it can be excluded from the URL.
const BARE_URL_PATTERN = /(?<!\(|\[)(https?:\/\/[^\s<>[\]"'`（）。，、；：！？…—]+)/g;
// Fullwidth and CJK punctuation that should never be treated as part of a URL.
const TRAILING_FULLWIDTH_PUNCT = /[（）。，、；：！？…—\u3000-\u303F\uFF00-\uFFEF]+$/;
const SAFE_INLINE_IMAGE_DATA = /^data:image\/(?:png|jpe?g|gif|webp|bmp);base64,/i;
const EDITABLE_FENCE_PATTERN = /```editable[ \t]*\r?\n[\s\S]*?\r?\n```/g;

const markdownRemarkWorkflows = [[remarkGfm, { singleTilde: false }], remarkMath];
const markdownRehypeWorkflows = [
  rehypeRaw,
  rehypeKatex,
  [rehypeSanitize, customSchema],
];

export function markdownUrlTransform(value: string): string {
  return SAFE_INLINE_IMAGE_DATA.test(value) ? value : defaultUrlTransform(value);
}

const MarkdownRenderContext = createContext<{
  isStreaming: boolean;
  markSources: ChatSource[];
  artifacts: ConversationArtifact[];
  conversationId?: string;
  historyId?: string;
  onCiteMessage?: (text: string) => void;
}>({
  isStreaming: false,
  markSources: [],
  artifacts: EMPTY_CONVERSATION_ARTIFACTS,
});

const SOURCE_PREVIEW_TEXT_LIMIT = 280;

function getSourceBrandName(source: ChatSource) {
  const subtitle = getSourceSubtitle(source).replace(/^www\./i, "");
  return subtitle || getSourceLabel(source);
}

function getSourcePreviewText(source: ChatSource) {
  const text = getSourceEvidenceText(source).replace(/\s+/g, " ").trim();
  return text.length > SOURCE_PREVIEW_TEXT_LIMIT
    ? `${text.slice(0, SOURCE_PREVIEW_TEXT_LIMIT).trimEnd()}…`
    : text;
}

function SourceBrandIcon({ source }: { source: ChatSource }) {
  const [hasFaviconError, setHasFaviconError] = useState(false);
  const faviconUrl = getSourceFaviconUrl(source);
  const label = getSourceBrandName(source);

  return (
    <span className="md-source-chip-icon" aria-hidden="true">
      {faviconUrl && !hasFaviconError ? (
        <img
          src={faviconUrl}
          alt=""
          loading="lazy"
          referrerPolicy="no-referrer"
          onError={() => setHasFaviconError(true)}
        />
      ) : (
        <span>{label.slice(0, 1).toLocaleUpperCase() || "S"}</span>
      )}
    </span>
  );
}

function SourcePreviewCard({ source }: { source: ChatSource }) {
  const sourceHref = getSourceHref(source);
  const sourceUrl = isExternalSource(source) && /^https?:\/\//i.test(sourceHref)
    ? sourceHref
    : "";
  const previewText = getSourcePreviewText(source);

  return (
    <div className="md-source-preview">
      <div className="md-source-preview-brand">
        <SourceBrandIcon source={source} />
        <span>{getSourceBrandName(source)}</span>
      </div>
      <strong className="md-source-preview-title">{getSourceLabel(source)}</strong>
      {previewText && <p className="md-source-preview-summary">{previewText}</p>}
      {sourceUrl && <span className="md-source-preview-url">{sourceUrl}</span>}
    </div>
  );
}

function getSourceIndex(href: any) {
  if (typeof href !== "string") {
    return "";
  }
  const prefix = SOURCE_PREFIXES.find((item) => href.startsWith(item));
  return prefix ? href.slice(prefix.length) : "";
}

function normalizeBoldBareUrls(content: string) {
  return content.replace(BOLD_BARE_URL_PATTERN, (match, url) => {
    if (url.includes("](")) {
      return match;
    }
    const href = url.startsWith("www.") ? `https://${url}` : url;
    return `**[${url}](${href})**`;
  });
}

/**
 * Wraps bare URLs in Markdown link syntax and strips trailing fullwidth/CJK
 * punctuation (e.g. Chinese parentheses, periods) that should not be part of
 * the URL but would otherwise be picked up by remark-gfm's autolink detection.
 */
function normalizeBareUrls(content: string) {
  return content.replace(BARE_URL_PATTERN, (url) => {
    const cleanUrl = url.replace(TRAILING_FULLWIDTH_PUNCT, "");
    if (!cleanUrl) return url;
    const suffix = url.slice(cleanUrl.length);
    return `[${cleanUrl}](${cleanUrl})${suffix}`;
  });
}

function normalizeMarkdownForDisplay(content: string) {
  const normalizeFragment = (fragment: string) =>
    normalizeBoldBareUrls(
      normalizeBareUrls(
        normalizeArtifactFileLinks(
          stripRedundantSourceUrls(normalizeSourceMarkers(fragment)),
        ),
      ),
    );
  let result = "";
  let cursor = 0;

  for (const match of content.matchAll(EDITABLE_FENCE_PATTERN)) {
    const index = match.index ?? 0;
    result += normalizeFragment(content.slice(cursor, index));
    result += match[0];
    cursor = index + match[0].length;
  }

  return result + normalizeFragment(content.slice(cursor));
}

const ImageComponent = (props: any) => {
  const { t } = useTranslation();
  const [imageLoadError, setImageLoadError] = useState(false);
  const [previewVisible, setPreviewVisible] = useState(false);
  const [resolvedSrc, setResolvedSrc] = useState(() =>
    resolveCoreAssetUrl(props.src || ""),
  );

  useEffect(() => {
    let cancelled = false;
    const rawSrc = props.src || "";
    setImageLoadError(false);
    setResolvedSrc(resolveCoreAssetUrl(rawSrc));

    resolveMarkdownImageUrlAsync(rawSrc)
      .then((url) => {
        if (!cancelled && url) {
          setImageLoadError(false);
          setResolvedSrc(url);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setResolvedSrc(resolveCoreAssetUrl(rawSrc));
        }
      });

    return () => {
      cancelled = true;
    };
  }, [props.src]);

  if (imageLoadError || !resolvedSrc) {
    return null;
  }

  const { node: _node, src: _src, ...imageProps } = props;

  return (
    <Image
      {...imageProps}
      src={resolvedSrc}
      preview={{
        visible: previewVisible,
        onVisibleChange: setPreviewVisible,
      }}
      role="button"
      tabIndex={0}
      aria-label={props.alt || t("chat.previewImage")}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          setPreviewVisible(true);
        }
      }}
      onError={() => setImageLoadError(true)}
      onLoad={() => setImageLoadError(false)}
    />
  );
};

const CodeComponent = (props: any) => {
  const { children, className, inline, ...rest } = props;
  const code = String(children ?? "").replace(/\n$/, "");
  const language = getLanguageFromClassName(className);
  const highlighted = useMemo(
    () => (!inline ? highlightCode(code, language) : ""),
    [code, inline, language],
  );

  if (inline || !highlighted) {
    return (
      <code {...rest} className={className}>
        {children}
      </code>
    );
  }

  return (
    <code
      {...rest}
      className={classnames(className, `language-${language}`)}
      data-language={language}
      dangerouslySetInnerHTML={{ __html: highlighted }}
    />
  );
};

const PreComponent = (props: any) => {
  const {
    isStreaming,
    markSources,
    conversationId,
    historyId,
    onCiteMessage,
  } = useContext(MarkdownRenderContext);
  const child = Array.isArray(props.children) ? props.children[0] : props.children;

  if (isValidElement(child)) {
    const childProps = child.props as {
      children?: unknown;
      className?: string;
    };
    const rawLanguage = getRawLanguageFromClassName(childProps.className);
    const language = getLanguageFromClassName(childProps.className);
    const code = String(childProps.children ?? "").replace(/\n$/, "");

    if (rawLanguage === "editable" && !isStreaming && conversationId && historyId) {
      return (
        <EditableBlock
          key={`${conversationId}:${historyId}`}
          value={code}
          conversationId={conversationId}
          historyId={historyId}
          sources={markSources}
          onCiteSelection={onCiteMessage}
        />
      );
    }

    if (rawLanguage === "html" || rawLanguage === "htm") {
      return <HtmlBlock code={code} isStreaming={isStreaming} />;
    }

    if (language === "mermaid") {
      return <MermaidBlock code={code} isStreaming={isStreaming} />;
    }
  }

  return <pre {...props} />;
};

function inlineArtifactBlobType(artifact: ConversationArtifact): string {
  return artifact.content_type === "json"
    ? "application/json"
    : "text/plain;charset=utf-8";
}

function ArtifactFileLink({
  children,
  href,
  filename,
  pending,
}: {
  children: ReactNode;
  href: string;
  filename: string;
  pending?: boolean;
}) {
  const { t } = useTranslation();
  const desktop = hasDesktopFileBridge();
  const ready = Boolean(href) && !pending;

  const runAction = useCallback(
    async (action: () => Promise<unknown>, failedKey: string) => {
      try {
        await action();
      } catch (error) {
        console.error(error);
        message.error(t(failedKey));
      }
    },
    [t],
  );

  const items = useMemo<MenuProps["items"]>(() => {
    const fileActions: MenuProps["items"] = [
      {
        key: "saveAs",
        disabled: !ready,
        label: t("chat.fileSaveAs"),
        onClick: () => {
          void runAction(
            () => saveArtifactFileAs(href, filename),
            "chat.fileSaveFailed",
          );
        },
      },
      {
        key: "download",
        disabled: !ready,
        label: t("chat.fileDownload"),
        onClick: () => {
          void runAction(
            () => downloadArtifactFile(href, filename),
            "chat.fileDownloadFailed",
          );
        },
      },
    ];
    if (!desktop) {
      return fileActions;
    }
    return [
      {
        key: "reveal",
        disabled: !ready,
        label: isAppleDesktopPlatform()
          ? t("chat.fileShowInFinder")
          : t("chat.fileShowInFolder"),
        onClick: () => {
          void runAction(
            () => revealArtifactFile(href, filename),
            "chat.fileRevealFailed",
          );
        },
      },
      { type: "divider" },
      ...(fileActions ?? []),
    ];
  }, [desktop, filename, href, ready, runAction, t]);

  const content = pending || !href ? (
    <span className="md-file-link md-file-link--pending">{children}</span>
  ) : (
    <a
      className="md-file-link"
      href={href}
      download={filename || undefined}
      rel="noreferrer"
      onClick={(event: MouseEvent<HTMLAnchorElement>) => {
        event.preventDefault();
        void runAction(
          () => downloadArtifactFile(href, filename),
          "chat.fileDownloadFailed",
        );
      }}
    >
      {children}
    </a>
  );

  return (
    <Dropdown
      trigger={["contextMenu"]}
      menu={{ items }}
      overlayClassName="md-file-link-dropdown"
    >
      {content}
    </Dropdown>
  );
}

const LinkComponent = (props: any) => {
  const { isStreaming, markSources, artifacts } = useContext(
    MarkdownRenderContext,
  );
  const href = typeof props.href === "string" ? props.href : "";
  const managedFile = href.includes("/static-files/");
  const artifactFileId = getFileIdFromHref(href);
  const linkedArtifact = artifactFileId
    ? findArtifactByFileId(artifacts, artifactFileId)
    : undefined;
  const artifactFilename = linkedArtifact
    ? getArtifactFilename(linkedArtifact)
    : "";
  const artifactSignSource = linkedArtifact
    ? getArtifactSignSource(linkedArtifact)
    : "";
  const inlineArtifact = Boolean(
    linkedArtifact && isInlineDownloadableArtifact(linkedArtifact),
  );
  const inlineText =
    inlineArtifact && linkedArtifact
      ? getArtifactTextContent(linkedArtifact)
      : "";
  const inlineBlobType =
    inlineArtifact && linkedArtifact
      ? inlineArtifactBlobType(linkedArtifact)
      : "";
  const [resolvedHref, setResolvedHref] = useState(() =>
    managedFile || artifactFileId ? "" : href,
  );
  useEffect(() => {
    let cancelled = false;
    if (artifactFileId) {
      if (inlineArtifact) {
        const blob = new Blob([inlineText], { type: inlineBlobType });
        const objectUrl = URL.createObjectURL(blob);
        setResolvedHref(objectUrl);
        return () => {
          cancelled = true;
          URL.revokeObjectURL(objectUrl);
        };
      }
      if (!artifactSignSource) {
        setResolvedHref("");
        return () => {
          cancelled = true;
        };
      }
      setResolvedHref("");
      const applySignedUrl = (url: string) => {
        if (cancelled) return;
        setResolvedHref(isBrowserDownloadHref(url) ? url : "");
      };
      resolveMarkdownImageUrlAsync(artifactSignSource)
        .then(applySignedUrl)
        .catch(() => {
          applySignedUrl(resolveCoreAssetUrl(artifactSignSource));
        });
      return () => {
        cancelled = true;
      };
    }
    if (!managedFile) {
      setResolvedHref(href);
      return () => {
        cancelled = true;
      };
    }
    setResolvedHref("");
    resolveMarkdownImageUrlAsync(href).then((url) => {
      if (!cancelled) setResolvedHref(url);
    }).catch(() => {
      if (!cancelled) setResolvedHref("");
    });
    return () => {
      cancelled = true;
    };
  }, [
    href,
    managedFile,
    artifactFileId,
    artifactSignSource,
    inlineArtifact,
    inlineText,
    inlineBlobType,
  ]);
  const sourceIndex = getSourceIndex(href);

  if (sourceIndex) {
    const source = findSourceByCitationId(markSources, sourceIndex);
    const sourceHref = source ? getSourceHref(source) : "";
    const label = source
      ? getSourceLabel(source)
      : typeof props.title === "string" && props.title
        ? props.title
        : "Source";
    const chipContent = source ? (
      <>
        <SourceBrandIcon source={source} />
        <span className="md-source-chip-label">{getSourceBrandName(source)}</span>
      </>
    ) : (
      <span className="md-source-chip-label">{label}</span>
    );
    const chip = source ? (
      <a
        className={classnames("md-source-chip", {
          "md-source-chip--pending": isStreaming,
          "md-source-chip--clickable": true,
        })}
        href={sourceHref}
        target="_blank"
        rel="noopener noreferrer"
        aria-label={label}
        title={label}
      >
        {chipContent}
      </a>
    ) : (
      <span className="md-source-chip md-source-chip--pending">
        {chipContent}
      </span>
    );

    if (isStreaming || !source) {
      return chip;
    }

    return (
      <Popover
        mouseEnterDelay={0.2}
        placement="top"
        classNames={{ root: "md-source-popover" }}
        content={<SourcePreviewCard source={source} />}
      >
        {chip}
      </Popover>
    );
  }

  if (artifactFileId) {
    return (
      <ArtifactFileLink
        href={resolvedHref}
        filename={artifactFilename}
        pending={!resolvedHref}
      >
        {props.children}
      </ArtifactFileLink>
    );
  }

  if (managedFile) {
    return (
      <ArtifactFileLink
        href={resolvedHref}
        filename={basenameFromPath(href)}
        pending={!resolvedHref}
      >
        {props.children}
      </ArtifactFileLink>
    );
  }

  return (
    <a
      href={resolvedHref || undefined}
      target="_blank"
      rel="noreferrer"
    >
      {props.children}
    </a>
  );
};

const ScriptComponent = () => null;

const LiComponent = (props: any) => {
  const children = Array.isArray(props.children)
    ? props.children.filter((item: any) => item !== "\n")
    : props.children;

  return <li>{children}</li>;
};

/**
 * antd Image renders a <div>, but react-markdown wraps standalone images in <p>.
 * Use a <div> for paragraphs that contain images to avoid invalid <p><div> nesting.
 */
const ParagraphComponent = (props: any) => {
  const { node: _node, children, ...rest } = props;
  const childList = Array.isArray(children) ? children : children != null ? [children] : [];
  const hasBlockImage = childList.some(
    (child) => isValidElement(child) && child.type === ImageComponent,
  );

  if (hasBlockImage) {
    return (
      <div className="md-paragraph md-paragraph--with-image" {...rest}>
        {children}
      </div>
    );
  }

  return <p {...rest}>{children}</p>;
};

const defaultMarkdownComponents = {
  a: LinkComponent,
  script: ScriptComponent,
  li: LiComponent,
  p: ParagraphComponent,
  img: ImageComponent,
  pre: PreComponent,
  code: CodeComponent,
};

const MarkdownViewer = memo((props: any) => {
  const {
    children,
    className = "",
    components: customComponents,
    sources = [],
    IS_STREAMING,
    conversationId: conversationIdProp,
    historyId,
    onCiteMessage,
    ...markdownProps
  } = props;
  const normalizedChildren =
    typeof children === "string"
      ? normalizeMarkdownForDisplay(children)
      : children;

  const activeConversationId = useTaskCenterStore(
    (state) => state.activeConversationId,
  );
  const conversationId = conversationIdProp ?? activeConversationId;
  const artifacts = useTaskCenterStore((state) =>
    conversationId
      ? (state.artifactsByConversation[conversationId] ??
        EMPTY_CONVERSATION_ARTIFACTS)
      : EMPTY_CONVERSATION_ARTIFACTS,
  );
  const loadConversationArtifacts = useTaskCenterStore(
    (state) => state.loadConversationArtifacts,
  );
  const hasFileIdLink =
    typeof children === "string" && conversationHasFileIdLink(children);

  useEffect(() => {
    if (!conversationId || !hasFileIdLink) return;
    const existing =
      useTaskCenterStore.getState().artifactsByConversation[conversationId];
    if (existing && existing.length > 0) return;
    void loadConversationArtifacts(conversationId);
  }, [conversationId, hasFileIdLink, loadConversationArtifacts]);

  const [markSources, setMarkSources] = useState<ChatSource[]>([]);

  useEffect(() => {
    if (sources && sources.length > 0) {
      setMarkSources(sources);
    }
  }, [sources]);

  const renderContextValue = useMemo(
    () => ({
      isStreaming: Boolean(IS_STREAMING),
      markSources,
      artifacts,
      conversationId,
      historyId,
      onCiteMessage,
    }),
    [IS_STREAMING, markSources, artifacts, conversationId, historyId, onCiteMessage],
  );

  const markdownComponents = useMemo(
    () => ({
      ...defaultMarkdownComponents,
      ...customComponents,
    }),
    [customComponents],
  );

  return (
    <div
      className={classnames("rag-markdown", {
        [className]: !!className,
      })}
    >
      <MarkdownRenderContext.Provider value={renderContextValue}>
        <Markdown
          {...markdownProps}
          urlTransform={markdownProps.urlTransform ?? markdownUrlTransform}
          remarkPlugins={markdownRemarkWorkflows}
          rehypePlugins={markdownRehypeWorkflows}
          components={markdownComponents}
        >
          {normalizedChildren || ""}
        </Markdown>
      </MarkdownRenderContext.Provider>
    </div>
  );
});

export default MarkdownViewer;
