import type { DataNode } from "antd/es/tree";
import type { TFunction } from "i18next";
import type { ScanV2TreeNode } from "../../utils/scanAccessors";
import { getFileUpdateMeta } from "../../utils/status";
import {
  getScanTreeNodeKey,
  getTreeNodeUpdateState,
  isSelectableScanTreeDocument,
  type SyncTreeDataNode,
} from "../../utils/scanTree";

function toSyncTreeDataNodes(
  nodes: ScanV2TreeNode[],
  t: TFunction,
): SyncTreeDataNode[] {
  return nodes.map((node) => {
    const children = node.children
      ? toSyncTreeDataNodes(node.children, t)
      : undefined;
    const updateState = getTreeNodeUpdateState(node);
    const updateMeta = getFileUpdateMeta(updateState, t);
    const updateText =
      `${node.update_desc || node.source_state || ""}`.trim() ||
      updateMeta.text;
    const hasUpdateStatus =
      typeof node.has_update === "boolean" ||
      Boolean(node.update_type || node.update_desc || node.source_state);
    const title = node.display_name || node.title || node.object_key || node.key;

    return {
      key: getScanTreeNodeKey(node),
      treeKey: `${node.key}`,
      objectKey: node.object_key,
      nodeRef: node.node_ref,
      isLeaf: !node.has_children,
      disableCheckbox: !isSelectableScanTreeDocument(node),
      title: (
        <div className="data-source-sync-tree-file">
          <div className="data-source-sync-tree-file-main">
            <span>{title}</span>
            {hasUpdateStatus ? (
              <span
                className={`data-source-sync-tree-chip data-source-sync-tree-chip-${updateState}`}
                title={updateText}
              >
                {updateText}
              </span>
            ) : null}
          </div>
        </div>
      ),
      childrenLoaded: Boolean(node.children),
      children,
    };
  });
}

export function buildSyncTreeData(
  syncTreeNodes: ScanV2TreeNode[],
  t: TFunction,
): DataNode[] {
  return toSyncTreeDataNodes(syncTreeNodes, t);
}
