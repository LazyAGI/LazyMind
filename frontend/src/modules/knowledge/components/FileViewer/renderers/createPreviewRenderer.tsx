import { useEffect, useRef, useState, type ComponentType } from "react";
import { Button, Spin } from "antd";
import { useTranslation } from "react-i18next";

// Handle module loading here so import failures follow the preview error path,
// rather than escaping React.lazy into the surrounding page's error boundary.
export function createPreviewRenderer<P extends { onPreviewError?: () => void }>(
  load: () => Promise<{ default: ComponentType<P> }>,
) {
  let loaded: ComponentType<P> | undefined;
  return function PreviewRenderer(props: P) {
    const { t } = useTranslation();
    const [Renderer, setRenderer] = useState(() => loaded);
    const [failed, setFailed] = useState(false);
    const [attempt, setAttempt] = useState(0);
    const onError = useRef(props.onPreviewError);
    onError.current = props.onPreviewError;

    useEffect(() => {
      if (Renderer) return;
      let current = true;
      void load().then((module) => {
        loaded = module.default;
        if (current) setRenderer(() => module.default);
      }).catch(() => {
        if (!current) return;
        setFailed(true);
        onError.current?.();
      });
      return () => { current = false; };
    }, [Renderer, attempt]);

    if (Renderer) return <Renderer {...props} />;
    if (failed) return <div role="alert">
      <p>{t("knowledge.previewLoadFailed")}</p>
      <Button onClick={() => { setFailed(false); setAttempt(value => value + 1); }}>
        {t("common.retry")}
      </Button>
    </div>;
    return <Spin />;
  };
}
