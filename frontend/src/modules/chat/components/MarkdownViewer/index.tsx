import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import classnames from "classnames";
import "katex/dist/katex.min.css";
import { Popover } from "antd";
import rehypeSanitize from "rehype-sanitize";
import "./markdown.scss";
import "./index.scss";
import {
  createContext,
  isValidElement,
  memo,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { customSchema } from "./config";
import rehypeRaw from "rehype-raw";
import {
  resolveCoreAssetUrl,
  resolveMarkdownImageUrlAsync,
} from "@/modules/knowledge/utils/imageUrl";
import MermaidBlock from "./MermaidBlock";
import { getLanguageFromClassName, highlightCode } from "./syntaxHighlight";

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
  markSources: any[];
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
  const [imageLoadError, setImageLoadError] = useState(false);
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

  return (
    <img
      {...props}
      src={resolvedSrc}
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
    const language = getLanguageFromClassName(childProps.className);

    if (language === "mermaid") {
      return (
        <MermaidBlock
          code={String(childProps.children ?? "").replace(/\n$/, "")}
          isStreaming={isStreaming}
        />
      );
    }
  }

  return <pre {...props} />;
};

const LinkComponent = (props: any) => {
  const { isStreaming, markSources } = useContext(MarkdownRenderContext);
  const href = props.href;
  const sourceIndex = getSourceIndex(href);

  if (sourceIndex) {
    if (isStreaming) {
      return (
        <span
          className="md-segment-index"
          style={{ backgroundColor: "var(--color-text-description)" }}
        >
          {props.children}
        </span>
      );
    }

    return (
      <Popover
        title={props.title || ""}
        content={
          <div className="md-content-card">
            <div className="md-content-card-content">
              <MarkdownViewer>
                {
                  markSources.find(
                    (source) => String(source.index) === sourceIndex,
                  )?.content
                }
              </MarkdownViewer>
            </div>
          </div>
        }
      >
        <span className="md-segment-index">{props.children}</span>
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
      ? normalizeBoldBareUrls(normalizeBareUrls(children))
      : children;

  const [markSources, setMarkSources] = useState<any[]>([]);

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
