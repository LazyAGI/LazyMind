import { Segmented } from "antd";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import MarkdownViewer from "@/modules/knowledge/components/MarkdownViewer";

interface RenderMarkdownProps {
  fileData: ArrayBuffer;
  renderMarkdown?: (markdown: string) => ReactNode;
}

type MarkdownView = "preview" | "source";

const RenderMarkdown = ({ fileData, renderMarkdown }: RenderMarkdownProps) => {
  const { t } = useTranslation();
  const [view, setView] = useState<MarkdownView>("preview");
  const markdown = useMemo(() => new TextDecoder().decode(fileData), [fileData]);

  useEffect(() => {
    setView("preview");
  }, [fileData]);

  return (
    <div className="file-viewer-markdown-container">
      <div className="file-viewer-markdown-toolbar">
        <Segmented<MarkdownView>
          aria-label={t("knowledge.markdownViewMode")}
          options={[
            { label: t("chat.markdownRender"), value: "preview" },
            { label: t("chat.markdownSource"), value: "source" },
          ]}
          value={view}
          onChange={setView}
        />
      </div>
      <div className="file-viewer-markdown-content">
        {view === "preview" ? (
          renderMarkdown ? renderMarkdown(markdown) : <MarkdownViewer>{markdown}</MarkdownViewer>
        ) : (
          <pre className="file-viewer-markdown-source">{markdown}</pre>
        )}
      </div>
    </div>
  );
};

export default RenderMarkdown;
