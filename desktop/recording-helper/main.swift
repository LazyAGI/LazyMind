import AppKit
import ScreenCaptureKit
import ApplicationServices
import Darwin

// A separately signed application owns TCC permissions. No recording is written to disk.
final class Recorder: NSObject, NSApplicationDelegate, @unchecked Sendable {
    var connection: FileHandle!
    let output = DispatchQueue(label: "recording.rpc.output")
    var session: String?
    var frames = [[String: Any]](), events = [[String: Any]]()
    var frameBytes = 0, eventBytes = 0
    var started = Date(), lastMove = -1.0
    var timer: Timer?, lifetime: Timer?
    var tap: CFMachPort?, tapSource: CFRunLoopSource?
    var filter: SCContentFilter?, configuration: SCStreamConfiguration?
    var bounds = CGRect.zero
    var capturing = false, selecting = false
    let arguments = CommandLine.arguments
    func argument(_ name: String) -> String? {
        guard let index = arguments.firstIndex(of: name), index + 1 < arguments.count else { return nil }
        return arguments[index + 1]
    }
    func applicationDidFinishLaunching(_ notification: Notification) {
        guard let socketPath = argument("--socket"), let token = argument("--token"),
              let parent = argument("--parent"), let pid = Int32(parent), socketPath.utf8.count < 104 else { exit(1) }
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        var address = sockaddr_un(); address.sun_family = sa_family_t(AF_UNIX)
        withUnsafeMutableBytes(of: &address.sun_path) { target in
            target.initializeMemory(as: UInt8.self, repeating: 0)
            socketPath.utf8CString.withUnsafeBytes { source in target.copyBytes(from: source) }
        }
        let result = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
        }
        guard result == 0 else { exit(1) }
        connection = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
        send(["hello": token])
        lifetime = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { _ in
            if kill(pid, 0) != 0 { self.cleanup(); exit(0) }
        }
        DispatchQueue.global().async {
            var pending = Data()
            while true {
                let bytes = self.connection.availableData
                if bytes.isEmpty { DispatchQueue.main.async { self.cleanup(); exit(0) }; return }
                pending.append(bytes)
                if pending.count > 65536 { exit(1) }
                while let end = pending.firstIndex(of: 10) {
                    let line = pending.prefix(upTo: end); pending.removeSubrange(...end)
                    guard let value = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else { exit(1) }
                    DispatchQueue.main.async { self.handle(value) }
                }
            }
        }
    }
    func send(_ message: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: message) else { return }
        output.async { try? self.connection.write(contentsOf: data + Data([10])) }
    }
    func reply(_ id: Int, _ value: [String: Any] = [:]) { send(["id": id, "result": value]) }
    func fail(_ id: Int, _ code: String) { send(["id": id, "error": code]) }
    func handle(_ request: [String: Any]) {
        guard let id = request["id"] as? Int, let method = request["method"] as? String else { return }
        switch method {
        case "permissions":
            let screen = CGPreflightScreenCaptureAccess(), input = AXIsProcessTrusted()
            if !input { _ = AXIsProcessTrustedWithOptions([kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary) }
            if !screen {
                // ScreenCaptureKit registers the app in modern macOS privacy settings.
                // Legacy CGRequestScreenCaptureAccess may deny without showing a prompt.
                Task { @MainActor in
                    _ = try? await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: false)
                    self.reply(id, ["screen": CGPreflightScreenCaptureAccess(), "input": AXIsProcessTrusted()])
                }
            } else { reply(id, ["screen": screen, "input": input]) }
        case "settings":
            let section = AXIsProcessTrusted() ? "Privacy_ScreenCapture" : "Privacy_Accessibility"
            NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.preference.security?\(section)")!)
            reply(id)
        case "start":
            guard session == nil && !selecting else { fail(id, "recording-busy"); return }
            guard AXIsProcessTrusted() else { fail(id, "recording-helper-input-permission"); return }
            guard CGPreflightScreenCaptureAccess() else { fail(id, "recording-helper-screen-permission"); return }
            selecting = true
            Task { await self.start(id) }
        case "stop":
            guard request["session_id"] as? String == session, session != nil else { fail(id, "recording-session-expired"); return }
            finish(id)
        case "cancel":
            if selecting { NSApp.abortModal() }
            cleanup(); reply(id)
        case "shutdown": cleanup(); reply(id); output.sync {}; exit(0)
        default: fail(id, "recording-method-unsupported")
        }
    }
    @MainActor func start(_ id: Int) async {
        defer { selecting = false }
        do {
            let content = try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: false)
            guard selecting else { fail(id, "recording-cancelled"); return }
            let windows = content.windows.filter { $0.windowLayer == 0 && !($0.title ?? "").isEmpty && $0.owningApplication?.processID != getpid() }
            let picker = NSPopUpButton(frame: NSRect(x: 0, y: 0, width: 480, height: 28))
            for (index, display) in content.displays.enumerated() {
                let item = NSMenuItem(title: "整个屏幕 \(index + 1)", action: nil, keyEquivalent: "")
                item.representedObject = display; picker.menu?.addItem(item)
            }
            for window in windows {
                let item = NSMenuItem(title: "窗口：\(window.owningApplication?.applicationName ?? "") — \(window.title ?? "")", action: nil, keyEquivalent: "")
                item.representedObject = window; picker.menu?.addItem(item)
            }
            guard picker.numberOfItems > 0 else { fail(id, "recording-no-sources"); return }
            let alert = NSAlert(); alert.messageText = "选择要录制的屏幕或窗口"
            alert.informativeText = "仅录制选择的画面；同时记录鼠标和键盘操作，输入内容会随录制交给模型。不会录音。结束后由 LazyMind 生成技能。"
            alert.accessoryView = picker; alert.addButton(withTitle: "开始录制"); alert.addButton(withTitle: "取消")
            NSApp.activate(ignoringOtherApps: true)
            guard alert.runModal() == .alertFirstButtonReturn, selecting else { fail(id, "recording-cancelled"); return }
            // NSPopUpButton.addItem(withTitle:) coalesces duplicate titles. Bind the
            // actual capture object to each item; list indices are not source identities.
            if let display = picker.selectedItem?.representedObject as? SCDisplay {
                filter = SCContentFilter(display: display, excludingWindows: [])
                bounds = CGDisplayBounds(display.displayID)
            } else if let window = picker.selectedItem?.representedObject as? SCWindow {
                filter = SCContentFilter(desktopIndependentWindow: window); bounds = window.frame
            } else { fail(id, "recording-no-sources"); return }
            let config = SCStreamConfiguration(); let ratio = min(1, 1280 / max(bounds.width, bounds.height))
            config.width = max(1, Int(bounds.width * ratio)); config.height = max(1, Int(bounds.height * ratio))
            config.showsCursor = true; config.capturesAudio = false; configuration = config
            frames = []; events = []; frameBytes = 0; eventBytes = 0; lastMove = -1
            session = UUID().uuidString; started = Date()
            guard installTap() else { cleanup(); fail(id, "recording-helper-input-permission"); return }
            try await sample()
            guard let current = session else { fail(id, "recording-cancelled"); return }
            timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { _ in
                guard self.session != nil else { return }
                self.send(["event": "tick", "session_id": current, "seconds": Int(Date().timeIntervalSince(self.started))])
                if Date().timeIntervalSince(self.started) >= 120 || self.frames.count >= 120 || self.frameBytes >= 20 * 1024 * 1024 {
                    self.finish(nil); return
                }
                if !self.capturing { Task { @MainActor in
                    do { try await self.sample() } catch { self.cleanup(); self.send(["event": "failed", "session_id": current, "error": "recording-capture-failed:\((error as NSError).domain):\((error as NSError).code)"]) }
                } }
            }
            reply(id, ["session_id": current, "startedAt": started.timeIntervalSince1970 * 1000])
            NSApp.hide(nil)
        } catch { cleanup(); fail(id, "recording-capture-failed:\((error as NSError).domain):\((error as NSError).code)") }
    }
    @MainActor func sample() async throws {
        guard let current = session, let filter = filter, let configuration = configuration, !capturing else { return }
        capturing = true; defer { capturing = false }
        let image = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: configuration)
        guard session == current else { return }
        // Normalize Retina/wide-gamut screenshots to an opaque, bounded sRGB frame.
        let scale = min(1, 1280 / Double(max(image.width, image.height)))
        let width = max(1, Int(Double(image.width) * scale)), height = max(1, Int(Double(image.height) * scale))
        guard let context = CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0,
                                      space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue) else { throw NSError(domain: "frame-context", code: 1) }
        context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
        guard let normalized = context.makeImage() else { throw NSError(domain: "frame-image", code: 1) }
        let bitmap = NSBitmapImageRep(cgImage: normalized)
        var encoded: Data?
        for quality in [0.65, 0.45, 0.25] {
            if let data = bitmap.representation(using: .jpeg, properties: [.compressionFactor: quality]), data.count < 290000 { encoded = data; break }
        }
        guard let jpeg = encoded else { throw NSError(domain: "frame-size", code: 1) }
        let value = "data:image/jpeg;base64," + jpeg.base64EncodedString()
        frames.append(["image": value, "seconds": Date().timeIntervalSince(started)]); frameBytes += value.utf8.count
    }
    func installTap() -> Bool {
        let types: [CGEventType] = [.leftMouseDown, .leftMouseUp, .rightMouseDown, .rightMouseUp, .otherMouseDown, .otherMouseUp, .mouseMoved, .leftMouseDragged, .rightMouseDragged, .scrollWheel, .keyDown, .keyUp, .flagsChanged]
        let mask = types.reduce(CGEventMask(0)) { $0 | (1 << $1.rawValue) }
        tap = CGEvent.tapCreate(tap: .cgSessionEventTap, place: .headInsertEventTap, options: .listenOnly, eventsOfInterest: mask, callback: { _, type, event, context in
            let owner = Unmanaged<Recorder>.fromOpaque(context!).takeUnretainedValue()
            if type == .tapDisabledByTimeout || type == .tapDisabledByUserInput { if let tap = owner.tap { CGEvent.tapEnable(tap: tap, enable: true) } }
            else { owner.collect(type, event) }
            return Unmanaged.passUnretained(event)
        }, userInfo: Unmanaged.passUnretained(self).toOpaque())
        guard let tap = tap else { return false }
        tapSource = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0)
        CFRunLoopAddSource(CFRunLoopGetMain(), tapSource, .commonModes)
        CGEvent.tapEnable(tap: tap, enable: true); return true
    }
    func collect(_ type: CGEventType, _ raw: CGEvent) {
        guard session != nil, events.count < 1500, eventBytes < 1500000 else { return }
        let seconds = Date().timeIntervalSince(started)
        guard seconds <= 120 else { return }
        let flags = raw.flags
        let modifiers = [(CGEventFlags.maskControl,"ctrl"),(.maskAlternate,"alt"),(.maskShift,"shift"),(.maskCommand,"meta")].filter { flags.contains($0.0) }.map { $0.1 }
        var event: [String: Any] = ["seconds": seconds, "source": "desktop", "modifiers": modifiers]
        if type == .keyDown || type == .keyUp || type == .flagsChanged {
            event["kind"] = type == .keyUp ? "keyup" : "keydown"
            event.merge(recordingKeyboardFields(raw)) { _, value in value }
        } else {
            let point = raw.location; guard bounds.contains(point) else { return }
            if type == .mouseMoved || type == .leftMouseDragged || type == .rightMouseDragged {
                guard seconds - lastMove >= 0.25 else { return }; lastMove = seconds; event["kind"] = "mousemove"
            } else if type == .scrollWheel { event["kind"] = "wheel"; event["rotation"] = raw.getIntegerValueField(.scrollWheelEventDeltaAxis1) }
            else { event["kind"] = [.leftMouseDown,.rightMouseDown,.otherMouseDown].contains(type) ? "mousedown" : "mouseup"; event["button"] = raw.getIntegerValueField(.mouseEventButtonNumber) }
            event["x"] = point.x; event["y"] = point.y; event["coordinate_space"] = "os_screen"
            event["normalized_x"] = (point.x - bounds.minX) / bounds.width; event["normalized_y"] = (point.y - bounds.minY) / bounds.height
        }
        guard let bytes = try? JSONSerialization.data(withJSONObject: event) else { return }
        eventBytes += bytes.count; events.append(event)
    }
    func finish(_ id: Int?) {
        guard let current = session else { if let id = id { fail(id,"recording-session-expired") }; return }
        let value: [String: Any] = ["session_id": current, "frames": frames, "evidence": ["events": events, "limitations": ["desktop_keyboard_events_not_committed_text", "desktop_keyboard_is_global_no_dom"]]]
        cleanup()
        if let id = id { reply(id,value) } else { send(["event":"ended", "result":value]) }
    }
    func cleanup() {
        timer?.invalidate(); timer = nil; selecting = false; session = nil
        if let tap = tap { CGEvent.tapEnable(tap: tap, enable: false); CFMachPortInvalidate(tap) }; tap = nil
        if let source = tapSource { CFRunLoopRemoveSource(CFRunLoopGetMain(), source, .commonModes) }; tapSource = nil
        frames = []; events = []; filter = nil; configuration = nil
    }
}
let application = NSApplication.shared
application.setActivationPolicy(.accessory)
let recorder = Recorder()
application.delegate = recorder
application.run()
