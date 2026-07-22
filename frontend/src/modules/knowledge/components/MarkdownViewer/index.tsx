import { useState, useEffect } from "react";
import Markdown from "react-markdown";
import rehypeRaw from "rehype-raw";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import classnames from "classnames";
import "katex/dist/katex.min.css";
import { Popover } from "antd";
import Rendering from "../Rendering";

import "../../../../components/MarkdownViewer/markdown.scss";
import "./index.scss";

const MarkdownViewer = (props: any) => {
  const { children, className = "" } = props;
  const [loading, setLoading] = useState(true);

  const escapeHtml = (text: string) => {
    return text
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  };

  const processContent = (content: string) => {
    return content.replace(/<[^>]*>/g, (match) => {
      return escapeHtml(match);
    });
  };

  const processedChildren =
    typeof children === "string" ? processContent(children) : children;

  useEffect(() => {
    setLoading(true);
    const timer = setTimeout(() => setLoading(false), 0);
    return () => clearTimeout(timer);
  }, [children]);

  return (
    <>
      {loading ? (
        <Rendering />
      ) : (
        <div
          className={classnames("rag-markdown", {
            [className]: !!className,
          })}
        >
          <Markdown
            {...props}
            remarkPlugins={[remarkGfm, remarkMath]}
            rehypePlugins={[rehypeRaw, rehypeKatex]}
            components={{
              a(props: any) {
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
              source(props: any) {
                const index = props.node?.properties?.index ?? "";
                const title = props.node?.properties?.title ?? "";
                const content = props.node?.properties?.content ?? "";
                return (
                  <Popover
                    title={title}
                    content={
                      <div className="md-content-card">
                        <div className="md-content-card-content">
                          <MarkdownViewer>{content}</MarkdownViewer>
                        </div>
                      </div>
                    }
                  >
                    <span className="md-segment-index">{index}</span>
                  </Popover>
                );
              },
              ...props.components,
            }}
          >
            {processedChildren || ""}
          </Markdown>
        </div>
      )}
    </>
  );
};

export default MarkdownViewer;
