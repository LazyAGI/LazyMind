import ExternalLink from '@/components/ExternalLink';
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
            <ExternalLink {...props} rel="noreferrer noopener" target="_blank">
              {children}
            </ExternalLink>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
