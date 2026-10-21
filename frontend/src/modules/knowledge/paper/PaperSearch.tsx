import { useEffect, useRef, type ChangeEvent } from "react";
import { Button, Input, type InputRef } from "antd";
import { CloseOutlined, SearchOutlined } from "@ant-design/icons";

interface Props {
  open: boolean;
  value: string;
  onOpenChange: (open: boolean) => void;
  onChange: (value: string) => void;
}

/** Keeps search in the toolbar, anchored beside the filter and export actions. */
export default function PaperSearch({ open, value, onOpenChange, onChange }: Props) {
  const container = useRef<HTMLDivElement>(null);
  const input = useRef<InputRef>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    input.current?.focus();
    const outside = (event: MouseEvent) => {
      if (event.target instanceof Node && !container.current?.contains(event.target)) onOpenChange(false);
    };
    document.addEventListener("mousedown", outside);
    return () => document.removeEventListener("mousedown", outside);
  }, [open, onOpenChange]);

  const close = () => {
    onOpenChange(false);
    requestAnimationFrame(() => trigger.current?.focus());
  };
  return <div ref={container} className={`paper-library-search ${open ? "is-open" : ""}`}>
    {open ? <div role="dialog" aria-label="搜索文献" className="paper-library-search-inline"
      onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); close(); } }}>
      <Input ref={input} aria-label="搜索文献" value={value} allowClear prefix={<SearchOutlined />}
        placeholder="题名、作者、DOI 或 arXiv" onChange={(event: ChangeEvent<HTMLInputElement>) => onChange(event.target.value)} onPressEnter={close} />
      <Button type="text" aria-label="收起搜索" icon={<CloseOutlined />} onClick={close} />
    </div> : <Button ref={trigger} type="text" className={`paper-library-tool ${value ? "is-active" : ""}`}
      icon={<SearchOutlined />} aria-label="搜索" aria-expanded={false} onClick={() => onOpenChange(true)}>
      搜索{value && <span className="paper-library-tool-dot" aria-hidden="true" />}
    </Button>}
  </div>;
}
