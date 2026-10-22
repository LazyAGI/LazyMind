// Initialize Prism and its grammars before shared editor chunks evaluate them.
import "prismjs";
import "./modules/chat/components/MarkdownViewer/syntaxHighlight";
import { createRoot } from "react-dom/client";
import App from "./App";
import GlobalErrorBoundary from "./components/GlobalErrorBoundary";
import "./index.scss";
import "./i18n";
import { installWorkflowEmbedFrameClock } from "./modules/chat/utils/workflowEmbedFrameClock";
import { installWorkflowEmbedBlobDownloads } from "./modules/chat/utils/workflowEmbedBlobDownload";

// Only explicit WorkBuddy embed URLs enable this host compatibility adapter.
const stopEmbedFrameClock = installWorkflowEmbedFrameClock();
const stopEmbedBlobDownloads = installWorkflowEmbedBlobDownloads();
if (import.meta.hot) import.meta.hot.dispose(() => {
  stopEmbedFrameClock();
  stopEmbedBlobDownloads();
});

const container = document.getElementById("app");
if (!container) throw new Error("Root element #app not found");
const root = createRoot(container);
root.render(
  <GlobalErrorBoundary>
    <App />
  </GlobalErrorBoundary>,
);
