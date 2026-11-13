import { useId, useRef, useState, type ChangeEvent, type KeyboardEvent, type RefObject } from 'react';
import { Button, Checkbox, Input, Popover, type InputRef } from 'antd';
import type { CheckboxChangeEvent } from 'antd/es/checkbox';
import { CloseOutlined, PlusOutlined } from '@ant-design/icons';
import { getPaperTags, isUserPaperTag, paperTagOrder, type PaperRecord } from './model';
import './PaperTags.scss';

export type PaperTagChange = { add: string[] } | { remove: string };

interface Props {
    paper: PaperRecord;
    collectionTags: string[];
    canWrite: boolean;
    disabled: boolean;
    onChange: (change: PaperTagChange) => Promise<void>;
}

const collapsedTagCount = 2;

export default function PaperTags({ paper, collectionTags, canWrite, disabled, onChange }: Props) {
    const [expanded, setExpanded] = useState(false);
    const [open, setOpen] = useState(false);
    const trigger = useRef<HTMLButtonElement>(null);
    const input = useRef<InputRef>(null);
    const tags = getPaperTags(paper.tags);
    const hiddenCount = tags.length - collapsedTagCount;
    if (!tags.length && !canWrite) return null;

    const close = () => {
        setOpen(false);
        trigger.current?.focus({ preventScroll: true });
    };
    const add = async (additions: string[]) => {
        await onChange({ add: additions });
        if (tags.length + additions.length > collapsedTagCount) setExpanded(true);
        close();
    };

    return <div className="paper-row-tags">
        {(expanded ? tags : tags.slice(0, collapsedTagCount)).map(tag => <span key={tag} className="paper-row-tag">
            <span className="paper-row-tag-name" title={tag}>{tag}</span>
            {canWrite && <button type="button" className="paper-row-tag-remove" aria-label={`移除标签 ${tag}`} disabled={disabled}
                onClick={async event => {
                    if (event.detail === 0) trigger.current?.focus({ preventScroll: true });
                    try { await onChange({ remove: tag }); }
                    catch { /* The library shows the save error in its notifications. */ }
                }}><CloseOutlined /></button>}
        </span>)}
        {hiddenCount > 0 && <button type="button" className="paper-row-tag-more" aria-expanded={expanded}
            aria-label={expanded ? '收起标签' : `展开其余 ${hiddenCount} 个标签`} onClick={() => setExpanded(!expanded)}>
            {expanded ? '收起' : `+${hiddenCount}`}
        </button>}
        {canWrite && <Popover trigger="click" placement="bottomLeft" arrow={false} destroyOnHidden
            overlayClassName="paper-library-tool-popover paper-tag-popover" open={open}
            onOpenChange={(next: boolean) => { if (!next || !disabled) setOpen(next); }}
            afterOpenChange={(visible: boolean) => { if (visible) input.current?.focus({ preventScroll: true }); }}
            content={<>{open && <PaperTagPicker title={paper.metadata.title} tags={tags} tagCount={paper.tags.length}
                collectionTags={collectionTags} disabled={disabled} input={input} onClose={close} onAdd={add} />}</>}>
            <button ref={trigger} type="button" className="paper-row-tag-add" aria-label="添加标签" aria-haspopup="dialog"
                aria-expanded={open} disabled={disabled && !open}><PlusOutlined /> 标签</button>
        </Popover>}
    </div>;
}

function PaperTagPicker({ title, tags, tagCount, collectionTags, disabled, input, onClose, onAdd }: {
    title: string;
    tags: string[];
    tagCount: number;
    collectionTags: string[];
    disabled: boolean;
    input: RefObject<InputRef>;
    onClose: () => void;
    onAdd: (tags: string[]) => Promise<void>;
}) {
    const headingId = useId();
    const scroll = useRef<HTMLDivElement>(null);
    const [search, setSearch] = useState('');
    const [draft, setDraft] = useState<string[]>([]);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState('');
    const applied = new Set(tags);
    const additions = draft.filter(tag => !applied.has(tag));
    const options = [...new Set([...collectionTags, ...draft])].sort(paperTagOrder.compare);
    const term = search.trim();
    const query = term.toLocaleLowerCase();
    const matches = options.filter(tag => tag.toLocaleLowerCase().includes(query));
    const canCreate = !!term && !options.some(tag => tag.toLocaleLowerCase() === query);
    const createError = canCreate && (!isUserPaperTag(term) ? '此名称用于系统标签'
        : /[;；]/.test(term) ? '标签名称不能包含分号'
        : new TextEncoder().encode(term).length > 255 ? '标签名称过长' : '');
    const overLimit = tagCount + additions.length > 100;
    const busy = disabled || saving;
    const groups = [
        { name: '已添加', locked: true, tags: matches.filter(tag => applied.has(tag)) },
        { name: '可添加', locked: false, tags: matches.filter(tag => !applied.has(tag)) },
    ];

    const updateSearch = (value: string) => {
        setSearch(value);
        if (scroll.current) scroll.current.scrollTop = 0;
    };
    const create = () => {
        if (busy || !canCreate || createError) return;
        setDraft([...draft, term]);
        setError('');
        updateSearch('');
        input.current?.focus({ preventScroll: true });
    };
    const save = async () => {
        if (busy || !additions.length || overLimit) return;
        setSaving(true);
        setError('');
        try { await onAdd(additions); }
        catch (cause) { setError(cause instanceof Error ? cause.message : '标签保存失败，请重试'); }
        finally { setSaving(false); }
    };

    return <div className="paper-tag-picker" role="dialog" aria-labelledby={headingId} onKeyDown={event => {
        if (event.key !== 'Escape' || event.nativeEvent.isComposing || event.keyCode === 229) return;
        event.stopPropagation();
        onClose();
    }}>
        <div className="paper-tag-picker-heading">
            <strong id={headingId}>添加标签</strong>
            <Button type="text" size="small" icon={<CloseOutlined />} aria-label="关闭添加标签" onClick={onClose} />
        </div>
        <p className="paper-tag-picker-title" title={title}>{title}</p>
        <Input ref={input} allowClear value={search} aria-label="搜索或新建标签" placeholder="搜索或新建标签"
            disabled={busy} onChange={(event: ChangeEvent<HTMLInputElement>) => updateSearch(event.target.value)} onPressEnter={(event: KeyboardEvent<HTMLInputElement>) => {
                if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                event.preventDefault();
                create();
            }} />
        <div className="paper-tag-picker-count">
            <span>论文集标签</span><span aria-live="polite">{term ? `匹配 ${matches.length} 个` : `${matches.length} 个`}</span>
        </div>
        <div ref={scroll} className="paper-tag-picker-scroll" role="region" aria-label="论文集标签列表">
            {groups.map(group => group.tags.length > 0 && <fieldset key={group.name} className="paper-tag-picker-group">
                <legend>{group.name}</legend>
                <div className="paper-tag-picker-grid">
                    {group.tags.map(tag => <Checkbox key={tag} disabled={busy || group.locked} checked={group.locked || draft.includes(tag)}
                        onChange={(event: CheckboxChangeEvent) => {
                            setDraft(event.target.checked ? [...draft, tag] : draft.filter(item => item !== tag));
                            setError('');
                        }}>{tag}</Checkbox>)}
                </div>
            </fieldset>)}
            {!matches.length && <p className="paper-tag-picker-empty">{term ? '没有匹配的标签' : '论文集还没有标签'}</p>}
        </div>
        {canCreate && !createError && <button type="button" className="paper-tag-picker-create" disabled={busy} onClick={create}>＋ 新建「{term}」</button>}
        {(createError || overLimit || error) && <p className="paper-tag-picker-error" role="alert">{createError || (overLimit ? '每篇论文最多 100 个标签' : error)}</p>}
        <div className="paper-tag-picker-footer">
            <span>待添加 {additions.length} 个标签</span>
            <div className="paper-tag-picker-actions">
                <Button size="small" autoInsertSpace={false} onClick={onClose}>取消</Button>
                <Button size="small" autoInsertSpace={false} type="primary" loading={saving} disabled={busy || !additions.length || overLimit} onClick={() => void save()}>添加</Button>
            </div>
        </div>
    </div>;
}
