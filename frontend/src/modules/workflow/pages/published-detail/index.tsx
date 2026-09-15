import { useEffect, useState } from "react";
import { Alert, Button, Spin } from "antd";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import StateGraphEditor from "../../components/StateGraphEditor";
import { parseWorkflowYaml } from "../../components/StateGraphEditor/core/workflowParser";
import { withWorkflowLayout } from "../../workflowPreview";
import { getWorkflowVersion, listWorkflowVersions, type WorkflowVersionContent } from "../../workflowDraftApi";

export default function PublishedWorkflowDetail() {
  const { workflowRef = "" } = useParams();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [content, setContent] = useState<WorkflowVersionContent>();
  const [error, setError] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let cancelled = false; setError(false); setContent(undefined);
    void (async () => {
      const versions = await listWorkflowVersions(workflowRef);
      const current = versions.find((version) => version.current) || versions[0];
      if (!current) throw new Error("Workflow version unavailable");
      const value = await getWorkflowVersion(workflowRef, current.revision_id);
      if (!cancelled) setContent(value);
    })().catch(() => { if (!cancelled) setError(true) });
    return () => { cancelled = true };
  }, [workflowRef, retry]);
  if (error) return <Alert type="error" showIcon message={t("admin.memoryResourceLocalLoadFailed")} action={<Button onClick={() => setRetry((value) => value + 1)}>{t("common.retry")}</Button>} />;
  if (!content) return <Spin />;
  return <StateGraphEditor key={content.revision_id} readonly initialWorkflowYaml={content.workflow_yaml_content} initialStateYaml={withWorkflowLayout(content.state_yaml_content, content.state_layout_content)} initialScenarioContent={content.scenario_content} initialScriptsContent={content.scripts_content} showEmptyHint={false} workflowName={parseWorkflowYaml(content.workflow_yaml_content)?.name || t("admin.memorySkillViewWorkflows")} onClose={() => navigate("/memory-management/skills?skillView=workflows")} />;
}
