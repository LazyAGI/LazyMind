import { useEffect, useRef, useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import type { SlotDef } from '../core/model';

interface Props {
  value: string;
  onChange: (val: string) => void;
  slots: SlotDef[];
  placeholder?: string;
}

// ── Serialization helpers ────────────────────────────────────────────────────

const SLOT_RE = /\{\{([a-zA-Z0-9_]+)\}\}/g;

type Segment = { kind: 'text'; text: string } | { kind: 'slot'; id: string };

function parseSegments(raw: string): Segment[] {
  const segs: Segment[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  SLOT_RE.lastIndex = 0;
  while ((m = SLOT_RE.exec(raw)) !== null) {
    if (m.index > last) segs.push({ kind: 'text', text: raw.slice(last, m.index) });
    segs.push({ kind: 'slot', id: m[1] });
    last = m.index + m[0].length;
  }
  if (last < raw.length) segs.push({ kind: 'text', text: raw.slice(last) });
  return segs;
}

/** Serialize the DOM back to a `{{slot_id}}` string. */
function serializeDOM(container: HTMLElement): string {
  let out = '';
  for (const child of container.childNodes) {
    if (child.nodeType === Node.TEXT_NODE) {
      out += child.textContent ?? '';
    } else if (child.nodeType === Node.ELEMENT_NODE) {
      const el = child as HTMLElement;
      if (el.dataset.slotId) {
        out += `{{${el.dataset.slotId}}}`;
      } else {
        out += el.textContent ?? '';
      }
    }
  }
  return out;
}

// ── DOM builder ──────────────────────────────────────────────────────────────

function buildChip(slotId: string, label: string, deleteAriaLabel: string, onDelete: (id: string) => void): HTMLElement {
  const chip = document.createElement('span');
  chip.className = 'pe-chip';
  chip.contentEditable = 'false';
  chip.dataset.slotId = slotId;

  const text = document.createElement('span');
  text.className = 'pe-chip-text';
  text.textContent = label;
  chip.appendChild(text);

  const btn = document.createElement('button');
  btn.className = 'pe-chip-del';
  btn.type = 'button';
      btn.setAttribute('aria-label', deleteAriaLabel);
  btn.innerHTML = '×';
  btn.addEventListener('mousedown', (e) => {
    e.preventDefault();
    onDelete(slotId);
  });
  chip.appendChild(btn);

  return chip;
}

function rebuildDOM(
  el: HTMLElement,
  raw: string,
  slotLabel: (id: string) => string,
  deleteAriaLabel: (label: string) => string,
  onDelete: (id: string) => void,
) {
  el.innerHTML = '';
  for (const seg of parseSegments(raw)) {
    if (seg.kind === 'text') {
      el.appendChild(document.createTextNode(seg.text));
    } else {
      el.appendChild(buildChip(seg.id, slotLabel(seg.id), deleteAriaLabel(slotLabel(seg.id)), onDelete));
    }
  }
}

// ── Component ────────────────────────────────────────────────────────────────

export default function PromptEditor({ value, onChange, slots, placeholder }: Props) {
  const { t } = useTranslation();
  const editorRef = useRef<HTMLDivElement>(null);
  const [dropdown, setDropdown] = useState<{ top: number; left: number } | null>(null);
  const [query, setQuery] = useState('');

  // Track the last value we emitted so we can ignore the echo-back from the parent.
  // This is the key to preventing the DOM-rebuild loop that breaks cursor position.
  const lastEmittedRef = useRef<string | null>(null);
  // Track whether we've done the initial mount render
  const mountedRef = useRef(false);

  const slotMap = new Map(slots.map((s) => [s.id, s]));
  const slotLabel = useCallback((id: string) => slotMap.get(id)?.label ?? id, [slots]);

  // ── Delete chip handler ──────────────────────────────────────────────────
  const handleDeleteChip = useCallback((slotId: string) => {
    const el = editorRef.current;
    if (!el) return;
    const chip = el.querySelector(`[data-slot-id="${slotId}"]`);
    if (chip) chip.parentNode?.removeChild(chip);
    const serialized = serializeDOM(el);
    lastEmittedRef.current = serialized;
    onChange(serialized);
  }, [onChange]);

  // ── Initial mount: build DOM from value ─────────────────────────────────
  useEffect(() => {
    const el = editorRef.current;
    if (!el) return;
    rebuildDOM(el, value, slotLabel, (lbl) => t('selfEvolutionRun.promptEditorDeleteSlot', { label: lbl }), handleDeleteChip);
    lastEmittedRef.current = value;
    mountedRef.current = true;
  // Only run on mount — intentionally empty deps except stable refs
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ── React to external value changes (e.g. undo, programmatic update) ────
  useEffect(() => {
    if (!mountedRef.current) return;
    const el = editorRef.current;
    if (!el) return;
    // If the incoming value is what we just emitted, skip — this is the echo-back
    if (value === lastEmittedRef.current) return;
    // Genuine external change: rebuild DOM
    rebuildDOM(el, value, slotLabel, (lbl) => t('selfEvolutionRun.promptEditorDeleteSlot', { label: lbl }), handleDeleteChip);
    lastEmittedRef.current = value;
  }, [value, slotLabel, handleDeleteChip]);

  // ── Emit serialized value ────────────────────────────────────────────────
  const emitChange = useCallback(() => {
    const el = editorRef.current;
    if (!el) return;
    const serialized = serializeDOM(el);
    lastEmittedRef.current = serialized;
    onChange(serialized);
  }, [onChange]);

  // ── Dropdown helpers ─────────────────────────────────────────────────────
  const openDropdown = useCallback(() => {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0) return;
    const range = sel.getRangeAt(0);
    const rect = range.getBoundingClientRect();
    const editorRect = editorRef.current?.getBoundingClientRect();
    if (!editorRect) return;
    setDropdown({
      top: rect.bottom - editorRect.top + 4,
      left: Math.max(0, rect.left - editorRect.left),
    });
    setQuery('');
  }, []);

  const closeDropdown = useCallback(() => setDropdown(null), []);

  // ── Insert chip at cursor ────────────────────────────────────────────────
  const insertSlot = useCallback((slotId: string) => {
    const el = editorRef.current;
    if (!el) return;
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0) return;

    // Remove the `{` trigger + any query text typed so far
    const anchor = sel.anchorNode;
    if (anchor && anchor.nodeType === Node.TEXT_NODE) {
      const text = anchor.textContent ?? '';
      const offset = sel.anchorOffset;
      const removeLen = 1 + query.length;
      const before = text.slice(0, Math.max(0, offset - removeLen));
      const after = text.slice(offset);
      anchor.textContent = before + after;
      const newRange = document.createRange();
      newRange.setStart(anchor, before.length);
      newRange.collapse(true);
      sel.removeAllRanges();
      sel.addRange(newRange);
    }

    // Insert chip node
    const chip = buildChip(slotId, slotLabel(slotId), t('selfEvolutionRun.promptEditorDeleteSlot', { label: slotLabel(slotId) }), handleDeleteChip);
    const finalSel = window.getSelection();
    if (finalSel && finalSel.rangeCount > 0) {
      const r = finalSel.getRangeAt(0);
      r.insertNode(chip);
      r.setStartAfter(chip);
      r.collapse(true);
      finalSel.removeAllRanges();
      finalSel.addRange(r);
    }

    emitChange();
    closeDropdown();
    el.focus();
  }, [query, slotLabel, handleDeleteChip, emitChange, closeDropdown]);

  // ── keydown ──────────────────────────────────────────────────────────────
  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    // ── Dropdown shortcuts ───────────────────────────────────────────────
    if (dropdown) {
      if (e.key === 'Escape') {
        e.preventDefault();
        closeDropdown();
        return;
      }
      if (e.key === 'Enter') {
        e.preventDefault();
        const filtered = slots.filter(
          (s) => s.id.includes(query) || (s.label ?? '').includes(query),
        );
        if (filtered.length > 0) insertSlot(filtered[0].id);
        return;
      }
    }
  }, [dropdown, query, slots, closeDropdown, insertSlot]);

  // ── input ────────────────────────────────────────────────────────────────
  const handleInput = useCallback(() => {
    const el = editorRef.current;
    if (!el) return;

    const sel = window.getSelection();
    const anchorNode = sel?.anchorNode;
    const offset = sel?.anchorOffset ?? 0;
    const text = anchorNode?.nodeType === Node.TEXT_NODE ? (anchorNode.textContent ?? '') : '';

    if (!dropdown) {
      // Detect `{` just typed
      if (text[offset - 1] === '{') {
        openDropdown();
      }
    } else {
      // Update query: everything after the last `{` before caret
      const triggerIdx = text.lastIndexOf('{', offset - 1);
      if (triggerIdx >= 0) {
        setQuery(text.slice(triggerIdx + 1, offset));
      } else {
        // `{` was deleted — close dropdown
        closeDropdown();
      }
    }

    emitChange();
  }, [dropdown, openDropdown, closeDropdown, emitChange]);

  // ── Close dropdown on outside click ─────────────────────────────────────
  useEffect(() => {
    if (!dropdown) return;
    const handler = (e: MouseEvent) => {
      if (editorRef.current && !editorRef.current.contains(e.target as Node)) {
        closeDropdown();
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [dropdown, closeDropdown]);

  const filteredSlots = slots.filter(
    (s) =>
      s.id.toLowerCase().includes(query.toLowerCase()) ||
      (s.label ?? '').toLowerCase().includes(query.toLowerCase()),
  );

  // isEmpty drives the CSS placeholder — read from DOM to avoid dependency on value
  const [isEmpty, setIsEmpty] = useState(!value);
  useEffect(() => {
    setIsEmpty(!value || value === '');
  }, [value]);

  return (
    <div className="pe-wrapper">
      <div
        ref={editorRef}
        className={`pe-editor${isEmpty ? ' pe-editor--empty' : ''}`}
        contentEditable
        suppressContentEditableWarning
        data-placeholder={placeholder ?? t('selfEvolutionRun.promptEditorPlaceholder')}
        onKeyDown={handleKeyDown}
        onInput={handleInput}
        onBlur={() => setTimeout(closeDropdown, 150)}
        aria-multiline="true"
        role="textbox"
        spellCheck={false}
      />
      {dropdown && filteredSlots.length > 0 && (
        <div
          className="pe-dropdown"
          style={{ top: dropdown.top, left: dropdown.left }}
          onMouseDown={(e) => e.preventDefault()}
        >
          {filteredSlots.map((s) => (
            <button
              key={s.id}
              className="pe-dropdown-item"
              type="button"
              onMouseDown={(e) => {
                e.preventDefault();
                insertSlot(s.id);
              }}
            >
              <span className="pe-dropdown-id">{s.id}</span>
              {s.label && <span className="pe-dropdown-label">{s.label}</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
