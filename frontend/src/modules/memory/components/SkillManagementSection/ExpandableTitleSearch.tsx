import { Input } from "antd";
import type { InputRef } from "antd/es/input";
import { SearchOutlined } from "@ant-design/icons";
import { useEffect, useId, useRef, useState } from "react";

interface ExpandableTitleSearchProps {
  value: string;
  placeholder: string;
  onChange: (value: string) => void;
  onSearch: (value: string) => void;
}

export default function ExpandableTitleSearch({
  value,
  placeholder,
  onChange,
  onSearch,
}: ExpandableTitleSearchProps) {
  const [open, setOpen] = useState(Boolean(value));
  const inputRef = useRef<InputRef>(null);
  const fieldRef = useRef<HTMLDivElement>(null);
  const fieldId = useId();

  useEffect(() => {
    if (!open) {
      return;
    }
    const timer = window.setTimeout(() => inputRef.current?.focus(), 180);
    return () => window.clearTimeout(timer);
  }, [open]);

  useEffect(() => {
    const node = fieldRef.current;
    if (!node) {
      return;
    }
    if (open) {
      node.removeAttribute("inert");
    } else {
      node.setAttribute("inert", "");
    }
  }, [open]);

  return (
    <div className={`memory-skill-title-search${open ? " is-open" : ""}`}>
      <button
        type="button"
        aria-label={placeholder}
        aria-expanded={open}
        aria-controls={fieldId}
        className={open || value ? "is-active" : undefined}
        onClick={() => setOpen((current) => !current)}
      >
        <SearchOutlined aria-hidden="true" />
      </button>
      <div
        id={fieldId}
        ref={fieldRef}
        className="memory-skill-title-search__field"
        aria-hidden={!open}
      >
        <Input.Search
          ref={inputRef}
          allowClear
          tabIndex={open ? undefined : -1}
          value={value}
          placeholder={placeholder}
          onChange={(event) => onChange(event.target.value)}
          onSearch={(next) => {
            onChange(next);
            onSearch(next);
          }}
        />
      </div>
    </div>
  );
}
