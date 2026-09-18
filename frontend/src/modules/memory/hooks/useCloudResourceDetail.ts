import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { DataNode } from "antd/es/tree";
import type { TreeProps } from "antd";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { isDesktopRuntime } from "@/runtime/mode";
import { getCloudSession, isCloudBusinessAvailable, LAZYMIND_CLOUD_SESSION_CHANGED_EVENT } from "@/runtime/cloud/session";
import { withWorkflowLayout } from "@/modules/workflow/workflowPreview";
import { splitMarkdownFrontMatter } from "../shared";
import { getCloudResource, getCloudResourceContent, getCloudResourceTree, type CloudResourceContent, type CloudResourceFile, type CloudResourceMetadata, type CloudResourceTree, type CloudResourceType } from "../cloudResourceApi";

function fileTree(files: CloudResourceFile[]): DataNode[] {
  const roots: DataNode[] = [];
  const folders = new Map<string, DataNode>();
  for (const file of files) {
    const parts = file.path.split("/");
    let children = roots;
    let prefix = "";
    parts.forEach((part, index) => {
      prefix += `${part}/`;
      if (index === parts.length - 1) { children.push({ title: part, key: file.path, isLeaf: true }); return }
      let folder = folders.get(prefix);
      if (!folder) { folder = { title: part, key: prefix, children: [], selectable: false }; folders.set(prefix, folder); children.push(folder) }
      children = folder.children!;
    });
  }
  return roots;
}

export function useCloudResourceDetail(resourceType: CloudResourceType) {
  const { resourceId = "" } = useParams();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [metadata, setMetadata] = useState<CloudResourceMetadata>();
  const [tree, setTree] = useState<CloudResourceTree>();
  const [selected, setSelected] = useState("");
  const [content, setContent] = useState<CloudResourceContent>();
  const [graphFiles, setGraphFiles] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [fileLoading, setFileLoading] = useState(false);
  const [error, setError] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [tab, setTab] = useState(resourceType === "workflow" ? "graph" : "documents");
  const rootRequest = useRef<AbortController>();
  const fileRequest = useRef<AbortController>();
  const cache = useRef(new Map<string, CloudResourceContent>());
  const back = resourceType === "skill" ? "/memory-management/skills" : "/memory-management/skills?skillView=workflows";

  const reload = useCallback(async () => {
    rootRequest.current?.abort(); fileRequest.current?.abort(); cache.current.clear();
    const controller = new AbortController(); rootRequest.current = controller;
    setMetadata(undefined); setTree(undefined); setContent(undefined); setGraphFiles({}); setSelected(""); setError(""); setSignedOut(false);
    if (!isDesktopRuntime()) { setLoading(false); return }
    setLoading(true);
    let businessAvailable = false;
    try {
      const session = await getCloudSession();
      if (controller.signal.aborted) return;
      businessAvailable = isCloudBusinessAvailable(session);
      if (!businessAvailable) { setSignedOut(true); return }
      const [meta, directory] = await Promise.all([getCloudResource(resourceType, resourceId, controller.signal), getCloudResourceTree(resourceType, resourceId, controller.signal)]);
      if (meta.resource_type !== resourceType || directory.resource_type !== resourceType || meta.content_hash !== directory.content_hash) throw new Error("Resource version changed");
      if (resourceType === "workflow") {
        const paths = ["workflow.yaml", "scenario/state.yml", "scenario/scenario.md", "scenario/layout.json"].filter((path) => directory.files.some((file) => file.path === path));
        const parts = await Promise.all(paths.map((path) => getCloudResourceContent(resourceType, resourceId, path, directory.content_hash, controller.signal)));
        if (controller.signal.aborted) return;
        const values: Record<string, string> = {};
        for (const part of parts) { cache.current.set(part.path, part); if (part.preview_status === "ready") values[part.path] = part.content }
        setGraphFiles(values);
      }
      if (controller.signal.aborted) return;
      setMetadata(meta); setTree(directory); setSelected(directory.entrypoint);
    } catch {
      if (!controller.signal.aborted) {
        if (businessAvailable) setError(t("admin.memoryCloudDetailFailed"));
        else setSignedOut(true);
      }
    } finally { if (!controller.signal.aborted) setLoading(false) }
  }, [resourceId, resourceType, t]);

  useEffect(() => {
    void reload();
    window.addEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, reload);
    return () => { rootRequest.current?.abort(); fileRequest.current?.abort(); cache.current.clear(); window.removeEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, reload) };
  }, [reload]);

  useEffect(() => {
    fileRequest.current?.abort(); setContent(undefined);
    if (!tree || !selected) return;
    const controller = new AbortController(); fileRequest.current = controller;
    setFileLoading(true); setError("");
    const cached = cache.current.get(selected);
    const read = cached ? Promise.resolve(cached) : getCloudResourceContent(resourceType, resourceId, selected, tree.content_hash, controller.signal);
    void read.then((value) => {
      if (controller.signal.aborted) return;
      if (cache.current.size >= 8 && !cache.current.has(selected)) cache.current.delete(cache.current.keys().next().value!);
      cache.current.set(selected, value); setContent(value);
    }).catch(() => { if (!controller.signal.aborted) setError(t("admin.memoryCloudDetailFailed")) })
      .finally(() => { if (!controller.signal.aborted) setFileLoading(false) });
    return () => controller.abort();
  }, [resourceType, resourceId, selected, tree, t]);

  const nodes = useMemo(() => fileTree(tree?.files || []), [tree]);
  const text = selected === "SKILL.md" ? splitMarkdownFrontMatter(content?.content || "")?.content ?? content?.content ?? "" : content?.content || "";
  const selectFile: TreeProps["onSelect"] = (keys, info) => {
    if (info.node.isLeaf && keys[0]) {
      setSelected(String(keys[0]));
      setTab("documents");
    }
  };
  const workflowStateYaml = graphFiles["scenario/state.yml"]
    ? withWorkflowLayout(graphFiles["scenario/state.yml"], graphFiles["scenario/layout.json"])
    : "";
  return {
    t, metadata, tree, selected, content, graphFiles, loading, fileLoading, error, tab, setTab, reload, nodes, text,
    selectFile, workflowStateYaml,
    redirectTo: !isDesktopRuntime() || signedOut ? back : undefined,
    goBack: () => navigate(back),
  };
}
