import { Component, useEffect, useRef, useState, type ReactNode } from "react";
import i18n from "../../i18n";
import { BASENAME } from "../../globalState";
import "./index.scss";

interface GlobalErrorBoundaryProps {
  children: ReactNode;
}

interface GlobalErrorBoundaryState {
  hasError: boolean;
}

const messages = {
  "zh-CN": {
    eyebrow: "页面遇到了一点问题",
    title: "抱歉，页面暂时无法正常显示",
    description: "请点击重试，重新加载当前页面。未保存的内容可能丢失。",
    reload: "重试",
    retrying: "正在重试…",
    home: "返回首页",
  },
  "en-US": {
    eyebrow: "Something went wrong",
    title: "Sorry, this page cannot be displayed right now",
    description: "Try reloading this page. Unsaved changes may be lost.",
    reload: "Retry",
    retrying: "Retrying…",
    home: "Back to home",
  },
} as const;

export function GlobalErrorFallback({ onRetry = () => window.location.reload(), onHome = () => window.location.assign(BASENAME || "/") }: { onRetry?: () => void; onHome?: () => void } = {}) {
  const copy = /^en/i.test(i18n.language || "") ? messages["en-US"] : messages["zh-CN"];
  const pending = useRef(false);
  const [busy, setBusy] = useState(false);
  // Desktop initially keeps its renderer hidden until it has usable UI.
  useEffect(() => { window.lazymindDesktop?.notifyAppReady?.(); }, []);
  const navigate = (action: () => void) => {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    try { action(); } catch { pending.current = false; setBusy(false); }
  };

  return (
    <main className="global-error" role="alert" aria-live="assertive">
      <section className="global-error__card" aria-labelledby="global-error-title">
        <div className="global-error__icon" aria-hidden="true">!</div>
        <p className="global-error__eyebrow">{copy.eyebrow}</p>
        <h1 id="global-error-title">{copy.title}</h1>
        <p className="global-error__description">{copy.description}</p>
        <div className="global-error__actions">
          <button type="button" className="global-error__primary" autoFocus disabled={busy} onClick={() => navigate(onRetry)}>
            {busy ? copy.retrying : copy.reload}
          </button>
          <button type="button" className="global-error__secondary" disabled={busy} onClick={() => navigate(onHome)}>
            {copy.home}
          </button>
        </div>
      </section>
    </main>
  );
}

class GlobalErrorBoundary extends Component<
  GlobalErrorBoundaryProps,
  GlobalErrorBoundaryState
> {
  state: GlobalErrorBoundaryState = { hasError: false };

  static getDerivedStateFromError(): GlobalErrorBoundaryState {
    return { hasError: true };
  }

  componentDidCatch() {
    // Exception text can contain document contents or credentials.
    console.error("[GlobalErrorBoundary] PAGE_RENDER_FAILED");
  }

  render() {
    return this.state.hasError ? <GlobalErrorFallback /> : this.props.children;
  }
}

export default GlobalErrorBoundary;
