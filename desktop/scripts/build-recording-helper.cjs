const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');
function buildRecordingHelper({ outDir, arch = process.arch, identity = process.env.LAZYMIND_RECORDING_SIGN_IDENTITY || '-', keychain = process.env.CSC_KEYCHAIN } = {}) {
  if (process.platform !== 'darwin') throw new Error('The native recording helper is built on macOS');
  const source = path.resolve(__dirname, '../recording-helper/main.swift');
  const keyboardSource = path.resolve(__dirname, '../recording-helper/keyboard.swift');
  const root = outDir || path.resolve(__dirname, '../build/recording-helper');
  const app = path.join(root, 'LazyMind Recorder.app');
  const digest = crypto.createHash('sha256').update(fs.readFileSync(source)).update(fs.readFileSync(keyboardSource)).update(fs.readFileSync(__filename)).update(`${arch}:${identity}:v1`).digest('hex');
  const manifest = path.join(app, 'Contents/Resources/build-id');
  // Reuse the exact signed artifact. Rebuilding unchanged ad-hoc bundles changes TCC identity.
  if (fs.existsSync(manifest) && fs.readFileSync(manifest, 'utf8') === digest) return app;
  fs.mkdirSync(root, { recursive: true });
  const staging = fs.mkdtempSync(path.join(root, '.helper-'));
  const built = path.join(staging, 'LazyMind Recorder.app');
  fs.mkdirSync(path.join(built, 'Contents/MacOS'), { recursive: true });
  fs.mkdirSync(path.join(built, 'Contents/Resources'), { recursive: true });
  const plist = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>ai.lazymind.recorder</string><key>CFBundleName</key><string>LazyMind Recorder</string><key>CFBundleDisplayName</key><string>LazyMind Recorder</string><key>CFBundleExecutable</key><string>LazyMindRecorder</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1</string><key>CFBundleShortVersionString</key><string>1.0</string><key>LSMinimumSystemVersion</key><string>14.0</string><key>LSUIElement</key><true/><key>NSScreenCaptureUsageDescription</key><string>录制您选择的画面并生成技能。</string><key>NSAccessibilityUsageDescription</key><string>记录技能演示中的鼠标键盘操作，输入内容随录制交给模型。</string></dict></plist>`;
  fs.writeFileSync(path.join(built, 'Contents/Info.plist'), plist);
  fs.writeFileSync(path.join(built, 'Contents/Resources/build-id'), digest);
  const target = arch === 'x64' ? 'x86_64' : 'arm64';
  execFileSync('xcrun', ['swiftc', '-swift-version', '5', '-O', '-target', `${target}-apple-macos14.0`, source, keyboardSource, '-o', path.join(built, 'Contents/MacOS/LazyMindRecorder'), '-framework', 'AppKit', '-framework', 'ScreenCaptureKit', '-framework', 'ApplicationServices'], { stdio: 'inherit' });
  const signing = ['--force', '--sign', identity, ...(identity === '-' ? ['--timestamp=none'] : ['--options', 'runtime', '--timestamp']), ...(keychain ? ['--keychain', keychain] : []), built];
  execFileSync('codesign', signing, { stdio: 'inherit' });
  execFileSync('codesign', ['--verify', '--strict', built]);
  fs.rmSync(app, { recursive: true, force: true }); fs.renameSync(built, app); fs.rmSync(staging, { recursive: true, force: true });
  return app;
}
module.exports = { buildRecordingHelper };
if (require.main === module) console.log(buildRecordingHelper({ outDir: process.argv[2] }));
