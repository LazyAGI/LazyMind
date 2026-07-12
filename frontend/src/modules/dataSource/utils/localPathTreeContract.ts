const LOCAL_PATH_TREE_PAGE_SIZE = 50;

export function buildLocalPathInitialChildrenRequest(agentId?: string) {
  return {
    connector_type: "local_fs",
    target_type: "local_path",
    // Empty target_ref is the connector contract for listing mounted roots.
    // "/" means "list children of the public root" and is not interchangeable.
    target_ref: "",
    agent_id: agentId || undefined,
    include_files: false,
    list_mode: "page",
    page_size: LOCAL_PATH_TREE_PAGE_SIZE,
  };
}

export function buildLocalPathChildrenRequest({
  targetRef,
  nodeRef,
  agentId,
}: {
  targetRef: string;
  nodeRef?: string;
  agentId?: string;
}) {
  return {
    connector_type: "local_fs",
    target_type: "local_path",
    target_ref: targetRef,
    node_ref: nodeRef || undefined,
    agent_id: agentId || undefined,
    include_files: false,
    list_mode: "page",
    page_size: LOCAL_PATH_TREE_PAGE_SIZE,
  };
}
