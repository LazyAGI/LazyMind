import React from "react";
import { createRoot } from "react-dom/client";
import { ConfigProvider } from "antd";
import "../../../src/i18n";
import PreferenceMemorySection from "../../../src/modules/memory/components/PreferenceMemorySection";
import "../../../src/modules/memory/index.scss";
createRoot(document.getElementById("root")!).render(<ConfigProvider><main style={{maxWidth: 920, margin: "40px auto"}}><PreferenceMemorySection /></main></ConfigProvider>);
