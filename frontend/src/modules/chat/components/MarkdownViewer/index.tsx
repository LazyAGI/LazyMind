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
import { isValidElement, useEffect, useMemo, useState } from "react";
import { customSchema } from "./config";
import rehypeRaw from "rehype-raw";
import {
  resolveCoreAssetUrl,
  resolveMarkdownImageUrlAsync,
} from "@/modules/knowledge/utils/imageUrl";
import MermaidBlock from "./MermaidBlock";
import { getLanguageFromClassName, highlightCode } from "./syntaxHighlight";

const SOURCE_PREFIXES = ["#source-", "#user-content-source-"];

function getSourceIndex(href: any) {
  if (typeof href !== "string") {
    return "";
  }
  const prefix = SOURCE_PREFIXES.find((item) => href.startsWith(item));
  return prefix ? href.slice(prefix.length) : "";
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
  const child = Array.isArray(props.children) ? props.children[0] : props.children;

  if (isValidElement(child)) {
    const childProps = child.props as {
      children?: unknown;
      className?: string;
    };
    const language = getLanguageFromClassName(childProps.className);

    if (language === "mermaid") {
      return (
        <MermaidBlock code={String(childProps.children ?? "").replace(/\n$/, "")} />
      );
    }
  }

  return <pre {...props} />;
};

const MarkdownViewer = (props: any) => {
  const { children, className = "", sources = [], IS_STREAMING } = props;

  const [markSources, setMarkSources] = useState<any[]>([]);

  useEffect(() => {
    if (sources && sources.length > 0) {
      setMarkSources(sources);
    }
  }, [sources]);

  return (
    <div
      className={classnames("rag-markdown", {
        [className]: !!className,
      })}
    >
      <Markdown
        {...props}
        remarkPlugins={[[remarkGfm, { singleTilde: false }], remarkMath]}
        rehypePlugins={[
          rehypeRaw,
          rehypeKatex,
          [rehypeSanitize, customSchema],
        ]}
        components={{
          a(props: any) {
            const href = props.href;
            const sourceIndex = getSourceIndex(href);
            if (sourceIndex) {
              if (IS_STREAMING) {
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
          },
          script() {
            return null;
          },
          li(props: any) {
            const children = Array.isArray(props.children)
              ? props.children.filter((item: any) => item !== "\n")
              : props.children;

            return <li>{children}</li>;
          },
          img: ImageComponent,
          pre: PreComponent,
          code: CodeComponent,
          ...props.components,
        }}
      >
        {children || ""}
      </Markdown>
    </div>
  );
};

export default MarkdownViewer;
