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

async function signAndStageEmbeddedRuntime(context) {
  if (macSigningMode !== "developer-id") {
    return;
  }

  const runtimeRoot = path.join(
    context.appOutDir,
    "LazyMind.app",
    "Contents",
    "Resources",
    "runtime",
  );
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

  // electron-osx-sign opens every file below the app concurrently even when
  // signIgnore matches it. Keep the large Python runtime outside the app while
  // electron-builder signs the Electron bundle, then restore and reseal it in
  // afterSign before manual notarization.
  const stagedRuntime = path.join(context.appOutDir, ".lazymind-runtime-for-signing");
  fs.rmSync(stagedRuntime, { recursive: true, force: true });
  fs.renameSync(runtimeRoot, stagedRuntime);
  stagedRuntimePaths.set(context.appOutDir, { runtimeRoot, stagedRuntime });
  console.log("Staged embedded runtime outside the app for Electron bundle signing");
}

async function restoreRuntimeAndFinalizeSignature(context) {
  if (macSigningMode !== "developer-id") {
    return;
  }

  const staged = stagedRuntimePaths.get(context.appOutDir);
  if (!staged || !fs.existsSync(staged.stagedRuntime)) {
    throw new Error("Staged embedded runtime was not found after Electron bundle signing");
  }
  fs.mkdirSync(path.dirname(staged.runtimeRoot), { recursive: true });
  fs.renameSync(staged.stagedRuntime, staged.runtimeRoot);
  stagedRuntimePaths.delete(context.appOutDir);

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

  if (notarizeMac) {
    const notarizationArchive = path.join(context.appOutDir, "LazyMind-notarize.zip");
    fs.rmSync(notarizationArchive, { force: true });
    try {
      await execFile("/usr/bin/ditto", [
        "-c",
        "-k",
        "--keepParent",
        appPath,
        notarizationArchive,
      ]);
      const { stdout } = await execFile("/usr/bin/xcrun", [
        "notarytool",
        "submit",
        notarizationArchive,
        "--apple-id",
        process.env.APPLE_ID,
        "--password",
        process.env.APPLE_APP_SPECIFIC_PASSWORD,
        "--team-id",
        process.env.APPLE_TEAM_ID,
        "--wait",
      ]);
      process.stdout.write(stdout);
      await execFile("/usr/bin/xcrun", ["stapler", "staple", appPath]);
      await execFile("/usr/bin/xcrun", ["stapler", "validate", appPath]);
    } finally {
      fs.rmSync(notarizationArchive, { force: true });
    }
  }
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
    identity: macSigningMode === "developer-id"
      ? undefined
      : (macSigningMode === "adhoc" ? "-" : null),
    hardenedRuntime: macSigningMode === "developer-id",
    entitlements: "assets/entitlements.mac.plist",
    entitlementsInherit: "assets/entitlements.mac.plist",
    // Notarization runs after the large runtime has been restored in afterSign.
    notarize: false,
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
  afterPack: signAndStageEmbeddedRuntime,
  afterSign: restoreRuntimeAndFinalizeSignature,
};
