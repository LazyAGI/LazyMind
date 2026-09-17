import { Alert, Button, Space, Spin, Tabs, Tag, Tree } from "antd";
import { ArrowLeftOutlined, ReloadOutlined } from "@ant-design/icons";
import { Navigate } from "react-router-dom";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import StateGraphEditor from "@/modules/workflow/components/StateGraphEditor";
import type { CloudResourceType } from "../../cloudResourceApi";
import { useCloudResourceDetail } from "../../hooks/useCloudResourceDetail";
import "./index.scss";

export default function CloudResourceDetail({ resourceType }: { resourceType: CloudResourceType }) {
  const { t, metadata, tree, selected, content, graphFiles, loading, fileLoading, error, tab, setTab, reload, nodes, text, selectFile, workflowStateYaml, redirectTo, goBack } = useCloudResourceDetail(resourceType);
  if (redirectTo) return <Navigate to={redirectTo} replace />;
  if (loading && !metadata && !tree && !error) {
    return <div className="cloud-resource-loading" role="status"><Spin /></div>;
  }
  const documents = <Spin spinning={fileLoading}>
    <div className="cloud-resource-document">
      <div className="cloud-resource-file-path">{selected}</div>
      {content?.preview_status === "ready" ? (/\.(md|markdown)$/i.test(selected)
        ? <Markdown remarkPlugins={[remarkGfm]} skipHtml components={{ a: ({ children, href }) => <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>, img: ({ alt }) => <span>{alt || t("admin.memoryCloudBinaryPreview")}</span> }}>{text}</Markdown>
        : <pre>{text}</pre>)
        : content ? <Alert type="info" showIcon message={t(content.preview_status === "too_large" ? "admin.memoryCloudLargePreview" : "admin.memoryCloudBinaryPreview")} /> : null}
    </div>
  </Spin>;
  return <div className="cloud-resource-detail">
    <header className="cloud-resource-detail-header">
      <Space wrap><Button icon={<ArrowLeftOutlined />} onClick={goBack}>{t("common.back")}</Button><h1>{metadata?.resource_name || t("admin.memoryCloudViewDetail")}</h1><Tag color="blue">{t("admin.memoryResourceCloud")}</Tag><Tag>{t("admin.memoryCloudReadOnly")}</Tag></Space>
      <Button icon={<ReloadOutlined />} onClick={() => void reload()}>{t("common.refresh")}</Button>
    </header>
    {error ? <Alert type="error" showIcon message={error} action={<Button onClick={() => void reload()}>{t("common.retry")}</Button>} /> : null}
    {loading ? <div className="cloud-resource-loading" role="status"><Spin /> {t("admin.memoryCloudLoading")}</div> : tree ? (
      <div className="cloud-resource-detail-body">
        <aside aria-label={t("admin.memoryCloudDirectory")}><Tree height={560} showLine blockNode defaultExpandAll selectedKeys={[selected]} treeData={nodes} onSelect={selectFile} /></aside>
        <main>
          {resourceType === "workflow" ? <Tabs activeKey={tab} onChange={setTab} items={[
            { key: "graph", label: t("admin.memoryCloudWorkflowDefinition"), children: graphFiles["workflow.yaml"] && graphFiles["scenario/state.yml"] ? <div className="cloud-resource-graph"><StateGraphEditor key={tree.content_hash} readonly initialWorkflowYaml={graphFiles["workflow.yaml"]} initialStateYaml={workflowStateYaml} initialScenarioContent={graphFiles["scenario/scenario.md"]} workflowName={metadata?.resource_name} showEmptyHint={false} /></div> : <Alert type="info" message={t("admin.memoryCloudGraphUnavailable")} /> },
            { key: "documents", label: t("admin.memoryCloudDocuments"), children: documents },
          ]} /> : documents}
        </main>
      </div>
    ) : null}
  </div>;
}
