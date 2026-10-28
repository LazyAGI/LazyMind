import { lazy, Suspense, useEffect, useState } from 'react';
import { Alert, Button, Spin } from 'antd';
import { DocumentServiceApi, normalizeProxyableUrl } from '../utils/request';
import type { PdfViewPosition } from '@/components/ui/RenderPdf';
import type { Attachment } from './libraryModel';

const FileViewer = lazy(() => import('../components/FileViewer'));

interface Props {
    datasetId: string;
    attachment: Attachment;
    position?: PdfViewPosition;
    onPositionChange: (position: PdfViewPosition) => void;
}

export default function PaperAttachmentPreview({ datasetId, attachment, position, onPositionChange }: Props) {
    const [file, setFile] = useState('');
    const [loading, setLoading] = useState(true);
    const [failed, setFailed] = useState(false);
    const [retry, setRetry] = useState(0);
    useEffect(() => {
        let cancelled = false;
        setFile('');
        setLoading(true);
        setFailed(false);
        void DocumentServiceApi().documentServiceGetDocument({ dataset: datasetId, document: attachment.contentRef! }).then(({ data }) => {
            if (cancelled) return;
            if (!data.file_url) throw new Error('原文件暂不可用');
            setFile(normalizeProxyableUrl(`${window.location.origin}/api/core${data.file_url}`));
        }).catch(() => { if (!cancelled) setFailed(true); }).finally(() => { if (!cancelled) setLoading(false); });
        return () => { cancelled = true; };
    }, [datasetId, attachment.contentRef, retry]);
    if (loading) return <div className="paper-attachment-preview-status" role="status"><Spin />正在加载附件预览…</div>;
    if (failed) return <Alert type="warning" showIcon message="附件预览暂时无法加载" description="可以重试，或通过阅读入口打开原文件。" action={<Button size="small" onClick={() => setRetry(value => value + 1)}>重试</Button>} />;
    return <div className="paper-attachment-preview"><Suspense fallback={<div className="paper-attachment-preview-status"><Spin />正在加载附件预览…</div>}><FileViewer file={file} fileName={attachment.name} pdfViewPosition={position} onPdfViewPositionChange={onPositionChange} /></Suspense></div>;
}
