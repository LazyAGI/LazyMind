import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import classnames from "classnames";
import "katex/dist/katex.min.css";
import { Image, Popover } from "antd";
import rehypeSanitize from "rehype-sanitize";
import { useTranslation } from "react-i18next";
import "../../../../components/MarkdownViewer/markdown.scss";
import "./index.scss";
import {
  createContext,
  isValidElement,
  memo,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { customSchema } from "./config";
import rehypeRaw from "rehype-raw";
import {
  resolveCoreAssetUrl,
  resolveMarkdownImageUrlAsync,
} from "@/modules/knowledge/utils/imageUrl";
import HtmlBlock from "./HtmlBlock";
import MermaidBlock from "./MermaidBlock";
import {
  getLanguageFromClassName,
  getRawLanguageFromClassName,
  highlightCode,
} from "./syntaxHighlight";
import {
  type ChatSource,
  findSourceByCitationId,
  getSourceEvidenceText,
  getSourceLabel,
  getSourceSubtitle,
  normalizeSourceMarkers,
  openSource,
  stripRedundantSourceUrls,
} from "@/modules/chat/utils/sourceAdapter";

const SOURCE_PREFIXES = ["#source-", "#user-content-source-"];
const BOLD_BARE_URL_PATTERN = /\*\*((?:https?:\/\/|www\.)[^\s*<>()]+)\*\*/g;
// Matches bare URLs that are NOT already inside Markdown link syntax [...](...)
// Captures trailing fullwidth/CJK punctuation so it can be excluded from the URL.
const BARE_URL_PATTERN = /(?<!\(|\[)(https?:\/\/[^\s<>[\]"'`（）。，、；：！？…—]+)/g;
// Fullwidth and CJK punctuation that should never be treated as part of a URL.
const TRAILING_FULLWIDTH_PUNCT = /[（）。，、；：！？…—\u3000-\u303F\uFF00-\uFFEF]+$/;

const markdownRemarkPlugins = [[remarkGfm, { singleTilde: false }], remarkMath];
const markdownRehypePlugins = [
  rehypeRaw,
  rehypeKatex,
  [rehypeSanitize, customSchema],
];

const MarkdownRenderContext = createContext<{
  isStreaming: boolean;
  markSources: ChatSource[];
}>({
  isStreaming: false,
  markSources: [],
});

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
  const { isStreaming } = useContext(MarkdownRenderContext);
  const child = Array.isArray(props.children) ? props.children[0] : props.children;

  if (isValidElement(child)) {
    const childProps = child.props as {
      children?: unknown;
      className?: string;
    };
    const rawLanguage = getRawLanguageFromClassName(childProps.className);
    const language = getLanguageFromClassName(childProps.className);
    const code = String(childProps.children ?? "").replace(/\n$/, "");

    if (rawLanguage === "html" || rawLanguage === "htm") {
      return <HtmlBlock code={code} isStreaming={isStreaming} />;
    }

    if (language === "mermaid") {
      return <MermaidBlock code={code} isStreaming={isStreaming} />;
    }
  }

  return <pre {...props} />;
};

function containsPointer(element: Element | null, x: number, y: number) {
  if (!element) return false;
  const rect = element.getBoundingClientRect();
  return x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom;
}

function useSourcePopoverHover() {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLSpanElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const pointerRef = useRef({ x: 0, y: 0 });
  const enteredPreviewRef = useRef(false);
  const closeTimerRef = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => {
    if (!open) return;

    const clearCloseTimer = () => {
      if (closeTimerRef.current) {
        clearTimeout(closeTimerRef.current);
        closeTimerRef.current = undefined;
      }
    };
    const popup = () => contentRef.current?.closest(".ant-popover") || null;
    const handlePointerMove = (event: PointerEvent) => {
      pointerRef.current = { x: event.clientX, y: event.clientY };
      if (containsPointer(popup(), event.clientX, event.clientY)) {
        enteredPreviewRef.current = true;
        clearCloseTimer();
        return;
      }
      if (containsPointer(triggerRef.current, event.clientX, event.clientY)) {
        clearCloseTimer();
        return;
      }
      if (enteredPreviewRef.current) {
        enteredPreviewRef.current = false;
        setOpen(false);
      }
    };
    const handleWheel = (event: WheelEvent) => {
      const content = contentRef.current?.querySelector<HTMLElement>(
        ".md-content-card-content",
      );
      if (!content || !containsPointer(popup(), event.clientX, event.clientY)) return;
      event.preventDefault();
      content.scrollBy({ left: event.deltaX, top: event.deltaY });
    };

    document.addEventListener("pointermove", handlePointerMove, { passive: true });
    document.addEventListener("wheel", handleWheel, { passive: false });
    return () => {
      document.removeEventListener("pointermove", handlePointerMove);
      document.removeEventListener("wheel", handleWheel);
      clearCloseTimer();
    };
  }, [open]);

  const onOpenChange = (nextOpen: boolean) => {
    if (nextOpen) {
      enteredPreviewRef.current = false;
      setOpen(true);
      return;
    }
    if (closeTimerRef.current) clearTimeout(closeTimerRef.current);
    closeTimerRef.current = setTimeout(() => {
      const { x, y } = pointerRef.current;
      const popup = contentRef.current?.closest(".ant-popover") || null;
      if (!containsPointer(triggerRef.current, x, y) && !containsPointer(popup, x, y)) {
        setOpen(false);
      }
    }, 180);
  };

  return { contentRef, onOpenChange, open, triggerRef };
}

const LinkComponent = (props: any) => {
  const { isStreaming, markSources } = useContext(MarkdownRenderContext);
  const sourcePopover = useSourcePopoverHover();
  const href = props.href;
  const sourceIndex = getSourceIndex(href);

  if (sourceIndex) {
    const source = findSourceByCitationId(markSources, sourceIndex);
    const label = source
      ? getSourceLabel(source)
      : typeof props.title === "string" && props.title
        ? props.title
        : "Source";
    const subtitle = source ? getSourceSubtitle(source) : "";
    const chip = (
      <span
        ref={sourcePopover.triggerRef}
        className={classnames("md-source-chip", {
          "md-source-chip--pending": isStreaming || !source,
          "md-source-chip--clickable": Boolean(source),
        })}
        role={source ? "link" : undefined}
        tabIndex={source ? 0 : undefined}
        onClick={() => source && openSource(source)}
        onKeyDown={(event) => {
          if (source && (event.key === "Enter" || event.key === " ")) {
            event.preventDefault();
            openSource(source);
          }
        }}
      >
        <span className="md-source-chip-label">{label}</span>
      </span>
    );

    if (isStreaming || !source) {
      return chip;
    }

    return (
      <Popover
        open={sourcePopover.open}
        onOpenChange={sourcePopover.onOpenChange}
        mouseEnterDelay={0.2}
        mouseLeaveDelay={0}
        classNames={{ root: "md-source-popover" }}
        title={subtitle ? `${label} · ${subtitle}` : label}
        content={
          <div ref={sourcePopover.contentRef} className="md-content-card">
            <div className="md-content-card-content">
              <MarkdownViewer>{getSourceEvidenceText(source)}</MarkdownViewer>
            </div>
          </div>
        }
      >
        {chip}
      </Popover>
    );
  }

  return (
    <a href={props.href} target="_blank">
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

const defaultMarkdownComponents = {
  a: LinkComponent,
  script: ScriptComponent,
  li: LiComponent,
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
  } = props;
  const normalizedChildren =
    typeof children === "string"
      ? normalizeBoldBareUrls(
          normalizeBareUrls(
            stripRedundantSourceUrls(normalizeSourceMarkers(children)),
          ),
        )
      : children;

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
    }),
    [IS_STREAMING, markSources],
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
          {...props}
          remarkPlugins={markdownRemarkPlugins}
          rehypePlugins={markdownRehypePlugins}
          components={markdownComponents}
        >
          {normalizedChildren || ""}
        </Markdown>
      </MarkdownRenderContext.Provider>
    </div>
  );
});

export default MarkdownViewer;
