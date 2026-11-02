import { useEffect, useId, useMemo, useRef, useState, type ChangeEvent } from "react";
import { Button } from "antd";
import { UndoOutlined, UploadOutlined } from "@ant-design/icons";
import { CustomLogoError, isCustomLogoDataUrl, prepareCustomLogo, type CustomLogoErrorCode } from "../customLogo";
import "./CustomLogoPicker.scss";

export interface CustomLogoPickerCopy {
  label: string;
  upload: string;
  replace: string;
  reset: string;
  hint: string;
  errors: Record<CustomLogoErrorCode, string>;
}

export interface CustomLogoPickerProps {
  id?: string;
  value?: string;
  onChange?: (value: string) => void;
  copy: CustomLogoPickerCopy;
  disabled?: boolean;
  onBusyChange?: (busy: boolean) => void;
}

/** A controlled Form field; only the parent owns the saved or draft logo. */
export function CustomLogoPicker(props: CustomLogoPickerProps) {
  const { copy } = props;
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<CustomLogoErrorCode>();
  const input = useRef<HTMLInputElement>(null);
  const mounted = useRef(true);
  const pendingLock = useRef(false);
  const operation = useRef(0);
  const valueRevision = useRef(0);
  const latestValue = useRef(props.value);
  const latestOnChange = useRef(props.onChange);
  const busyObserver = useRef<CustomLogoPickerProps["onBusyChange"]>();
  latestValue.current = props.value;
  latestOnChange.current = props.onChange;
  const descriptionId = useId();
  const preview = useMemo(() => isCustomLogoDataUrl(props.value) ? props.value : "/provider-icons/custom.svg", [props.value]);
  const custom = preview !== "/provider-icons/custom.svg";
  const action = custom ? copy.replace : copy.upload;
  const unavailable = props.disabled || pending;

  useEffect(() => {
    valueRevision.current += 1;
    setError(undefined);
  }, [props.value]);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      operation.current += 1;
      const observer = busyObserver.current;
      busyObserver.current = undefined;
      observer?.(false);
    };
  }, []);

  const choose = () => {
    if (!props.disabled && !pendingLock.current) input.current?.click();
  };
  const readFile = async (file?: File) => {
    if (props.disabled || pendingLock.current) return;
    setError(undefined);
    if (!file) { setError("empty"); return; }
    const request = ++operation.current;
    const revision = valueRevision.current;
    const initialValue = latestValue.current;
    pendingLock.current = true;
    setPending(true);
    // Pair notifications with this request's observer, never a newer model's callback.
    busyObserver.current = props.onBusyChange;
    busyObserver.current?.(true);
    const current = () => mounted.current && request === operation.current && revision === valueRevision.current && initialValue === latestValue.current;
    try {
      const value = await prepareCustomLogo(file);
      if (current()) latestOnChange.current?.(value);
    } catch (cause) {
      if (current()) setError(cause instanceof CustomLogoError ? cause.code : "decode");
    } finally {
      if (mounted.current && request === operation.current) {
        pendingLock.current = false;
        setPending(false);
        const observer = busyObserver.current;
        busyObserver.current = undefined;
        observer?.(false);
      }
    }
  };
  const changed = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    // Selecting the same file again after a failed upload must still trigger change.
    event.currentTarget.value = "";
    void readFile(file);
  };
  const reset = () => {
    if (!props.disabled && !pendingLock.current && props.value) {
      setError(undefined);
      latestOnChange.current?.("");
    }
  };

  return <div className="custom-logo-picker" role="group" aria-label={copy.label} aria-busy={pending} aria-describedby={descriptionId}>
    <input ref={input} type="file" accept="image/png,image/jpeg,image/webp" hidden disabled={unavailable} onChange={changed} />
    <button id={props.id} type="button" className="custom-logo-picker-preview" aria-label={`${copy.label}: ${action}`} disabled={unavailable} onClick={choose}>
      <img src={preview} alt="" onError={(event) => {
        if (event.currentTarget.getAttribute("src") !== "/provider-icons/custom.svg") event.currentTarget.src = "/provider-icons/custom.svg";
      }} />
    </button>
    <div className="custom-logo-picker-content">
      <span className="custom-logo-picker-label">{copy.label}</span>
      <div className="custom-logo-picker-actions">
        <Button htmlType="button" size="small" icon={<UploadOutlined aria-hidden="true" />} loading={pending} disabled={unavailable} onClick={choose}>{action}</Button>
        <Button htmlType="button" type="text" size="small" icon={<UndoOutlined aria-hidden="true" />} disabled={unavailable || !props.value} onClick={reset}>{copy.reset}</Button>
      </div>
      <p id={descriptionId} className="custom-logo-picker-hint">{copy.hint}</p>
      {error ? <p className="custom-logo-picker-error" role="alert">{copy.errors[error]}</p> : null}
    </div>
  </div>;
}

export default CustomLogoPicker;
