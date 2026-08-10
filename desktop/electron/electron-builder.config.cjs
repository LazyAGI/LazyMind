const childProcess = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const util = require("node:util");

const execFile = util.promisify(childProcess.execFile);
const delay = (milliseconds) => new Promise((resolve) => {
  setTimeout(resolve, milliseconds);
});
const stagedRuntimePaths = new Map();

const runtimeStage = process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE;
if (!runtimeStage) {
  throw new Error("LAZYMIND_DESKTOP_RUNTIME_STAGE is required");
}

const macSigningMode = process.env.LAZYMIND_DESKTOP_SIGNING_MODE || "adhoc";
if (!["adhoc", "developer-id", "none"].includes(macSigningMode)) {
  throw new Error(`Unsupported LAZYMIND_DESKTOP_SIGNING_MODE: ${macSigningMode}`);
}
const extraResources = [
  {
    from: runtimeStage,
    to: "runtime",
  },
];

const MACH_O_MAGICS = new Set([
  0xfeedface,
  0xfeedfacf,
  0xcefaedfe,
  0xcffaedfe,
  0xcafebabe,
  0xbebafeca,
  0xcafebabf,
  0xbfbafeca,
]);

function collectRuntimeMachOBinaries(root) {
  const binaries = [];
  const pending = [root];

  while (pending.length > 0) {
    const directory = pending.pop();
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const absolutePath = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        pending.push(absolutePath);
        continue;
      }
      if (!entry.isFile()) {
        continue;
      }

      const magic = Buffer.allocUnsafe(4);
      let descriptor;
      let bytesRead;
      try {
        descriptor = fs.openSync(absolutePath, "r");
        bytesRead = fs.readSync(descriptor, magic, 0, magic.length, 0);
      } finally {
        if (descriptor !== undefined) {
          fs.closeSync(descriptor);
        }
      }
      if (bytesRead === magic.length && MACH_O_MAGICS.has(magic.readUInt32BE(0))) {
        binaries.push(absolutePath);
      }
    }
  }

  return binaries.sort();
}

async function codesignWithRetry(args, binary) {
  const maximumAttempts = 3;
  for (let attempt = 1; attempt <= maximumAttempts; attempt += 1) {
    try {
      await execFile("/usr/bin/codesign", args);
      return;
    } catch (error) {
      if (attempt === maximumAttempts) {
        throw error;
      }
      console.warn(
        `codesign attempt ${attempt}/${maximumAttempts} failed for ${binary}; retrying`,
      );
      await delay(attempt * 1000);
    }
  }
}

async function developerIdSigningContext(context) {
  const { keychainFile } = await context.packager.codeSigningInfo.value;
  const identityArgs = ["find-identity", "-v", "-p", "codesigning"];
  if (keychainFile) {
    identityArgs.push(keychainFile);
  }
  const { stdout: identities } = await execFile("/usr/bin/security", identityArgs);
  const match = identities.match(
    /^\s*\d+\)\s+([0-9A-F]{40})\s+"Developer ID Application:/m,
  );
  if (!match) {
    throw new Error("No Developer ID Application signing identity was found");
  }
  return {
    entitlements: path.join(__dirname, "assets", "entitlements.mac.plist"),
    identity: match[1],
    keychainFile,
  };
}

function stageEmbeddedRuntime(appOutDir) {
  const runtimeRoot = path.join(
    appOutDir,
    "LazyMind.app",
    "Contents",
    "Resources",
    "runtime",
  );
  // electron-osx-sign opens every file below the app concurrently even when
  // signIgnore matches it. Keep the large Python runtime outside the app while
  // the Electron bundle is signed, then restore and reseal afterwards.
  const stagedRuntime = path.join(appOutDir, ".lazymind-runtime-for-signing");
  fs.rmSync(stagedRuntime, { recursive: true, force: true });
  fs.renameSync(runtimeRoot, stagedRuntime);
  stagedRuntimePaths.set(appOutDir, { runtimeRoot, stagedRuntime });
  console.log("Staged embedded runtime outside the app for Electron bundle signing");
  return { runtimeRoot, stagedRuntime };
}

function restoreEmbeddedRuntime(appOutDir) {
  const staged = stagedRuntimePaths.get(appOutDir);
  if (!staged || !fs.existsSync(staged.stagedRuntime)) {
    throw new Error("Staged embedded runtime was not found after Electron bundle signing");
  }
  fs.mkdirSync(path.dirname(staged.runtimeRoot), { recursive: true });
  fs.renameSync(staged.stagedRuntime, staged.runtimeRoot);
  stagedRuntimePaths.delete(appOutDir);
  return staged.runtimeRoot;
}

async function adhocSignAppBundle(appPath) {
  const frameworksDir = path.join(appPath, "Contents", "Frameworks");
  const electronFramework = path.join(frameworksDir, "Electron Framework.framework");
  const electronBinary = path.join(
    electronFramework,
    "Versions",
    "A",
    "Electron Framework",
  );
  if (fs.existsSync(electronBinary)) {
    await codesignWithRetry(["--force", "--sign", "-", "--timestamp=none", electronBinary], electronBinary);
  }
  if (fs.existsSync(electronFramework)) {
    await codesignWithRetry(["--force", "--sign", "-", "--timestamp=none", electronFramework], electronFramework);
  }

  for (const entry of fs.readdirSync(frameworksDir)) {
    const absolutePath = path.join(frameworksDir, entry);
    if (entry.endsWith(".app")) {
      await codesignWithRetry(
        ["--force", "--deep", "--sign", "-", "--timestamp=none", absolutePath],
        absolutePath,
      );
      continue;
    }
    if (entry.endsWith(".framework") && entry !== "Electron Framework.framework") {
      await codesignWithRetry(
        ["--force", "--sign", "-", "--timestamp=none", absolutePath],
        absolutePath,
      );
    }
  }

  await codesignWithRetry(["--force", "--sign", "-", "--timestamp=none", appPath], appPath);
}

async function signAndStageEmbeddedRuntime(context) {
  if (context.electronPlatformName !== "darwin" || macSigningMode === "none") {
    return;
  }

  const appPath = path.join(context.appOutDir, "LazyMind.app");
  const runtimeRoot = path.join(appPath, "Contents", "Resources", "runtime");

  if (macSigningMode === "developer-id") {
    const binaries = collectRuntimeMachOBinaries(runtimeRoot);
    console.log(`Signing ${binaries.length} embedded runtime Mach-O binaries`);
    const { entitlements, identity, keychainFile } = await developerIdSigningContext(context);

    // Keep a small amount of concurrency so timestamp requests are faster
    // without overwhelming Apple's timestamp service.
    const workers = Array.from({ length: Math.min(8, binaries.length) }, async () => {
      while (binaries.length > 0) {
        const binary = binaries.pop();
        const args = [
          "--sign",
          identity,
          "--force",
          "--timestamp",
          "--options",
          "runtime",
          "--entitlements",
          entitlements,
        ];
        if (keychainFile) {
          args.push("--keychain", keychainFile);
        }
        args.push(binary);
        await codesignWithRetry(args, binary);
      }
    });
    await Promise.all(workers);
    stageEmbeddedRuntime(context.appOutDir);
    return;
  }

  // electron-builder 24 treats identity "-" as a keychain name lookup and skips
  // signing when no matching identity exists. Perform ad-hoc signing ourselves
  // in afterPack because afterSign is skipped when electron-builder did not sign.
  stageEmbeddedRuntime(context.appOutDir);
  console.log("Ad-hoc signing Electron app bundle");
  await adhocSignAppBundle(appPath);
  restoreEmbeddedRuntime(context.appOutDir);
  await codesignWithRetry(["--force", "--sign", "-", "--timestamp=none", appPath], appPath);
  await execFile("/usr/bin/codesign", ["--verify", "--deep", "--strict", appPath]);
  console.log("Restored embedded runtime and resealed the outer ad-hoc app signature");
}

async function restoreRuntimeAndFinalizeSignature(context) {
  if (context.electronPlatformName !== "darwin" || macSigningMode !== "developer-id") {
    return;
  }

  restoreEmbeddedRuntime(context.appOutDir);

  const appPath = path.join(context.appOutDir, "LazyMind.app");
  const { entitlements, identity, keychainFile } = await developerIdSigningContext(context);
  const signArgs = [
    "--sign",
    identity,
    "--force",
    "--timestamp",
    "--options",
    "runtime",
    "--entitlements",
    entitlements,
  ];
  if (keychainFile) {
    signArgs.push("--keychain", keychainFile);
  }
  signArgs.push(appPath);
  await codesignWithRetry(signArgs, appPath);
  await execFile("/usr/bin/codesign", ["--verify", "--deep", "--strict", appPath]);
  console.log("Restored embedded runtime and resealed the outer app signature");
}
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
    // electron-builder 24 treats "-" as a keychain identity instead of ad-hoc.
    // Ad-hoc local signing runs explicitly in afterPack.
    identity: macSigningMode === "developer-id" ? undefined : null,
    hardenedRuntime: macSigningMode === "developer-id",
    entitlements: "assets/entitlements.mac.plist",
    entitlementsInherit: "assets/entitlements.mac.plist",
    // Notarization runs after the large runtime has been restored in afterSign.
    notarize: false,
  },
  dmg: {
    artifactName: "LazyMind-macos-${arch}.${ext}",
    sign: macSigningMode === "developer-id",
  },
  win: {
    icon: process.env.LAZYMIND_DESKTOP_WINDOWS_ICON || "assets/LazyMind.ico",
    target: ["zip"],
    requestedExecutionLevel: "asInvoker",
  },
  nsis: {
    oneClick: false,
    perMachine: false,
    allowElevation: false,
    uninstallDisplayName: "LazyMind",
    allowToChangeInstallationDirectory: false,
    installerLanguages: ["en_US", "zh_CN"],
    displayLanguageSelector: false,
    include: path.join(__dirname, "..", "installer", "installer.nsh"),
    artifactName: "LazyMind-windows-x64-installer.${ext}",
    differentialPackage: false,
    runAfterFinish: true,
  },
  afterPack: signAndStageEmbeddedRuntime,
  afterSign: restoreRuntimeAndFinalizeSignature,
};
