package common

import "net/http"

func init() {
	registerAdditionalError("workflow is not repairing", http.StatusConflict, 2004910)
	registerAdditionalError("cancel repair failed", http.StatusInternalServerError, 2004911)
	registerAdditionalError("workflow is not generating", http.StatusConflict, 2004912)
	registerAdditionalError("workflow is not editable", http.StatusConflict, 2004913)
	registerAdditionalError("state_yaml has no valid steps", http.StatusBadRequest, 2004914)
	registerAdditionalErrorAlias("请先修正 YAML 格式，再接入新增节点", "Fix invalid YAML before repairing the workflow", http.StatusBadRequest, 2004915)
	registerAdditionalErrorPattern("节点 %s 不存在，请刷新草稿后重试", "The workflow step no longer exists; refresh the draft and retry", http.StatusBadRequest, 2004916)
	registerAdditionalErrorPattern("请说明节点 %s 的用途和接入位置，或先配置节点并连接前后步骤", "Specify the step purpose and insertion point, or configure and connect it first", http.StatusBadRequest, 2004917)
	registerAdditionalError("node integration repair failed", http.StatusUnprocessableEntity, 2004918)
	registerAdditionalErrorPattern("decode interrupted workflow repair %s", "Failed to read the interrupted workflow repair", http.StatusInternalServerError, 2004919)
	registerAdditionalError("decode previous repair changes", http.StatusInternalServerError, 2004920)
	registerAdditionalErrorPattern("invalid workflow input correction for node %s", "Only unknown required input references may be explicitly replaced with existing materials", http.StatusBadRequest, 2004921)
}
