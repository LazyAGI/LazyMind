import React from "react";
import { createRoot } from "react-dom/client";
import { ConfigProvider } from "antd";
import i18n from "../../../src/i18n";
import PreferenceMemorySection from "../../../src/modules/memory/components/PreferenceMemorySection";
import "../../../src/modules/memory/index.scss";
createRoot(document.getElementById("root")!).render(<ConfigProvider><main style={{maxWidth: 920, margin: "40px auto"}}>
  <select aria-label="Fixture language" defaultValue={i18n.language} onChange={(event) => void i18n.changeLanguage(event.target.value)}>
    <option value="zh-CN">中文</option><option value="en-US">English</option>
  </select>
  <PreferenceMemorySection />
</main></ConfigProvider>);
