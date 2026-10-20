import { Input } from "antd";
import type { InputRef } from "antd/es/input";
import { SearchOutlined } from "@ant-design/icons";
import { useEffect, useRef, useState } from "react";

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

  useEffect(() => {
    if (!open) {
      return;
    }
    const timer = window.setTimeout(() => inputRef.current?.focus(), 180);
    return () => window.clearTimeout(timer);
  }, [open]);

  return (
    <div className={`memory-skill-title-search${open ? " is-open" : ""}`}>
      <button
        type="button"
        aria-label={placeholder}
        aria-expanded={open}
        className={open || value ? "is-active" : undefined}
        onClick={() => setOpen((current) => !current)}
      >
        <SearchOutlined aria-hidden="true" />
      </button>
      <div className="memory-skill-title-search__field">
        <Input.Search
          ref={inputRef}
          allowClear
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
