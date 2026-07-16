const path = require("node:path");

const runtimeStage = process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE;
if (!runtimeStage) {
  throw new Error("LAZYMIND_DESKTOP_RUNTIME_STAGE is required");
}

const extraResources = [
  {
    from: runtimeStage,
    to: "runtime",
  },
];
if (process.env.LAZYMIND_DESKTOP_WINDOWS_ICON) {
  extraResources.push({
    from: process.env.LAZYMIND_DESKTOP_WINDOWS_ICON,
    to: "LazyMind.ico",
  });
}

module.exports = {
  appId: "ai.lazymind.desktop",
  productName: "LazyMind",
  artifactName: "LazyMind-${os}-${arch}.${ext}",
  asar: true,
  directories: {
    output: process.env.LAZYMIND_DESKTOP_OUTPUT_DIR || path.join(__dirname, "..", "dist"),
    buildResources: process.env.LAZYMIND_DESKTOP_INSTALLER_RESOURCES || path.join(__dirname, "assets"),
  },
  files: [
    "src/**/*",
    "assets/**/*",
    "package.json",
  ],
  extraResources,
  mac: {
    category: "public.app-category.productivity",
    icon: "assets/LazyMind.icns",
    target: ["dir"],
    identity: null,
  },
  win: {
    icon: process.env.LAZYMIND_DESKTOP_WINDOWS_ICON || "assets/LazyMind.ico",
    target: ["zip"],
    requestedExecutionLevel: "asInvoker",
    signAndEditExecutable: Boolean(process.env.CSC_LINK),
  },
  nsis: {
    oneClick: false,
    perMachine: false,
    allowElevation: false,
    allowToChangeInstallationDirectory: false,
    installerLanguages: ["en_US", "zh_CN"],
    displayLanguageSelector: false,
    include: path.join(__dirname, "..", "installer", "installer.nsh"),
    artifactName: "LazyMind-windows-x64-installer.${ext}",
    differentialPackage: false,
    useZip: true,
    runAfterFinish: true,
  },
};
