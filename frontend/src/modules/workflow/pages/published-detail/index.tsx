import { Alert, Button, Spin } from "antd";
import StateGraphEditor from "../../components/StateGraphEditor";
import { usePublishedWorkflowDetail } from "../../hooks/usePublishedWorkflowDetail";

export default function PublishedWorkflowDetail() {
  const { t, content, error, reload, close, stateYaml, workflowName } = usePublishedWorkflowDetail();
  if (error) return <Alert type="error" showIcon message={t("admin.memoryResourceLocalLoadFailed")} action={<Button onClick={reload}>{t("common.retry")}</Button>} />;
  if (!content) return <Spin />;
  return <StateGraphEditor key={content.revision_id} readonly initialWorkflowYaml={content.workflow_yaml_content} initialStateYaml={stateYaml} initialScenarioContent={content.scenario_content} initialScriptsContent={content.scripts_content} showEmptyHint={false} workflowName={workflowName} onClose={close} />;
}
