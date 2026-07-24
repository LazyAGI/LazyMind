import ReactMarkdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import remarkGfm from "remark-gfm";

interface SafeReferenceMarkdownProps {
  content: string;
}

export default function SafeReferenceMarkdown({
  content,
}: SafeReferenceMarkdownProps) {
  return (
    <div className="memory-reference-markdown">
      <ReactMarkdown
        rehypePlugins={[rehypeSanitize]}
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          a: ({ children, ...props }) => (
            <a {...props} rel="noreferrer noopener" target="_blank">
              {children}
            </a>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
