const path = require("node:path");

const runtimeStage = process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE;
if (!runtimeStage) {
  throw new Error("LAZYMIND_DESKTOP_RUNTIME_STAGE is required");
}

const macSigningMode = process.env.LAZYMIND_DESKTOP_SIGNING_MODE || "adhoc";
if (!["adhoc", "developer-id", "none"].includes(macSigningMode)) {
  throw new Error(`Unsupported LAZYMIND_DESKTOP_SIGNING_MODE: ${macSigningMode}`);
}
const notarizeMac = process.env.LAZYMIND_DESKTOP_NOTARIZE === "true";
if (notarizeMac && macSigningMode !== "developer-id") {
  throw new Error("LAZYMIND_DESKTOP_NOTARIZE=true requires LAZYMIND_DESKTOP_SIGNING_MODE=developer-id");
}
if (notarizeMac && !process.env.APPLE_TEAM_ID) {
  throw new Error("APPLE_TEAM_ID is required for notarytool notarization");
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
    identity: macSigningMode === "developer-id"
      ? undefined
      : (macSigningMode === "adhoc" ? "-" : null),
    hardenedRuntime: macSigningMode === "developer-id",
    entitlements: "assets/entitlements.mac.plist",
    entitlementsInherit: "assets/entitlements.mac.plist",
    notarize: notarizeMac ? { teamId: process.env.APPLE_TEAM_ID } : false,
  },
  dmg: {
    artifactName: "LazyMind-macos-${arch}.${ext}",
    sign: false,
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
