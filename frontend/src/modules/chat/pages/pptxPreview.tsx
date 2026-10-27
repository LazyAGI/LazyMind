import { useEffect, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { Alert, Button, Spin } from 'antd';
import { WorkflowSessionApi } from '@/modules/chat/utils/request';
import { resolveCoreAssetUrl } from '@/modules/knowledge/utils/imageUrl';
import type { SlotVersionEntry } from '@/modules/chat/store/workflowPanel';

/** Resolve a pinned revision through the authorized session API, never a URL query. */
export default function PptxPreviewPage() {
  const { sessionId = '', slotId = '' } = useParams();
  const [query] = useSearchParams();
  const revision = Number(query.get('revision'));
  const index = Number(query.get('index') ?? '-1');
  const [file, setFile] = useState<{ url: string; name: string }>();
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    setFile(undefined);
    setError('');
    if (!Number.isInteger(revision) || revision < 1 || !Number.isInteger(index) || index < -1) {
      setError('PPT 版本参数无效');
      return () => controller.abort();
    }
    WorkflowSessionApi().getSlotItemVersions(sessionId, slotId, index, { signal: controller.signal })
      .then(response => {
        if (controller.signal.aborted) return;
        const versions: SlotVersionEntry[] = response.data?.data?.versions ?? [];
        const snapshot = versions.find(item => item.revision === revision)?.content_snapshot;
        let value = snapshot;
        if (typeof snapshot === 'string') {
          try { value = JSON.parse(snapshot); }
          catch { value = { url: snapshot }; }
        }
        if (typeof value === 'string') value = { url: value };
        const name = String(value?.filename ?? value?.name ?? 'presentation.pptx');
        const url = String(value?.url ?? value?.path ?? '');
        if (!url || !/\.pptx$/i.test(name)) throw new Error('该版本没有可预览的 PPTX 文件');
        setFile({ name, url: resolveCoreAssetUrl(url) });
      }).catch(reason => { if (!controller.signal.aborted) setError(reason?.message || 'PPT 加载失败'); });
    return () => controller.abort();
  }, [sessionId, slotId, revision, index]);
  return <div style={{ height: '100%', display: 'flex', flexDirection: 'column', padding: 16 }}>
    <div style={{ display: 'flex', gap: 16, alignItems: 'center', marginBottom: 12 }}>
      <Button href={`/workflow-runs/${encodeURIComponent(sessionId)}`}>返回工作流</Button>
      <span>{file?.name ?? 'PPTX'} · 版本 {revision}</span>
      {file && <Button href={file.url} download={file.name}>下载 PPTX</Button>}
    </div>
    {error ? <Alert type="error" message={error} /> : file
      ? <Alert type="info" message="网页暂不支持打开 PPTX，请下载后使用 PowerPoint 或 WPS 查看。" />
      : <Spin />}
  </div>;
}
