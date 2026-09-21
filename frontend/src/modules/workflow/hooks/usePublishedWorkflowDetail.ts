import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { parseWorkflowYaml } from "../components/StateGraphEditor/core/workflowParser";
import { withWorkflowLayout } from "../workflowPreview";
import { getWorkflowVersion, listWorkflowVersions, type WorkflowVersionContent } from "../workflowDraftApi";

export function usePublishedWorkflowDetail() {
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
  return {
    t, content, error,
    reload: () => setRetry((value) => value + 1),
    close: () => navigate("/memory-management/skills?skillView=workflows"),
    stateYaml: content ? withWorkflowLayout(content.state_yaml_content, content.state_layout_content) : "",
    workflowName: content ? parseWorkflowYaml(content.workflow_yaml_content)?.name || t("admin.memorySkillViewWorkflows") : "",
  };
}
