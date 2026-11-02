import { useEffect, useRef, useState } from 'react';
import { Alert, Checkbox, Input, Modal } from 'antd';
import { v4 as uuidv4 } from 'uuid';
import type { PaperLibraryData } from './model';
import { parseLibraryBackup, prepareLibraryRestore } from './libraryBackup';
import { readBlobText } from './zoteroImport';

interface Props {
    open: boolean;
    datasetId: string;
    disabled: boolean;
    onCancel: () => void;
    onRestore: (data: PaperLibraryData, key: string) => Promise<void>;
}
export default function PaperBackupRestore({ open, datasetId, disabled, onCancel, onRestore }: Props) {
    const [data, setData] = useState<PaperLibraryData | null>(null);
    const [includeReferences, setIncludeReferences] = useState(false);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const generation = useRef(0);
    const key = useRef(uuidv4());
    useEffect(() => {
        ++generation.current; setData(null); setError(''); setBusy(false); setIncludeReferences(false); key.current = uuidv4();
        return () => { ++generation.current; };
    }, [open, datasetId]);
    const crossLibrary = !!data?.backupDatasetId && data.backupDatasetId !== datasetId;
    const selectFile = async (file?: File) => {
        const scope = ++generation.current;
        setData(null); setError(''); key.current = uuidv4();
        if (!file) return;
        setBusy(true);
        try {
            if (file.size > 20 * 1024 * 1024) throw new Error('备份文件超过 20 MiB 上限');
            const parsed = parseLibraryBackup(await readBlobText(file));
            if (scope !== generation.current) return;
            setData(parsed); setIncludeReferences(parsed.backupDatasetId === datasetId);
        } catch (failure) {
            if (scope === generation.current) setError(failure instanceof Error ? failure.message : '无法读取文献库备份');
        } finally { if (scope === generation.current) setBusy(false); }
    };
    const restore = async () => {
        if (!data || busy || disabled) return;
        const scope = generation.current;
        setBusy(true); setError('');
        try {
            await onRestore(prepareLibraryRestore(data, datasetId, includeReferences, key.current), key.current);
            if (scope === generation.current) onCancel();
        } catch (failure) {
            if (scope === generation.current) setError(failure instanceof Error ? failure.message : '备份恢复失败，请重试');
        } finally { if (scope === generation.current) setBusy(false); }
    };
    return <Modal title="恢复文献库备份" open={open} onCancel={() => { if (!busy) onCancel(); }} onOk={restore} okText="确认恢复" cancelText="取消" confirmLoading={busy} okButtonProps={{ disabled: !data || disabled }} closable={!busy} maskClosable={!busy} keyboard={!busy} destroyOnHidden>
        <p>恢复题录、整理信息、笔记、批注和 PDF 引用。已有文献的当前信息会保留；备份不包含 PDF 文件。</p>
        <Input key={`${datasetId}:${open}`} type="file" accept=".json,application/json" aria-label="文献库备份文件" disabled={busy || disabled} onChange={event => { void selectFile(event.target.files?.[0]); }} />
        {data && <>
            <p role="status">{data.papers.length} 篇文献 · {data.folders.length} 个文件夹 · {data.notes.length} 条笔记 · {data.annotations?.length || 0} 条批注</p>
            <Checkbox checked={includeReferences && !crossLibrary} disabled={busy || disabled || crossLibrary} onChange={event => setIncludeReferences(event.target.checked)}>恢复当前知识库中已存在的 PDF 关联</Checkbox>
            {(crossLibrary || !includeReferences) && <p>PDF 附件信息会保留，恢复后需要重新选择 PDF 文件。</p>}
        </>}
        {error && <Alert type="error" showIcon message={error} />}
    </Modal>;
}
