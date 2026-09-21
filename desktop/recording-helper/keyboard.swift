import ApplicationServices

// This is event text, not a read of the focused input or an IME commit notification.
func recordingKeyboardFields(_ raw: CGEvent) -> [String: Any] {
    let code = Int(raw.getIntegerValueField(.keyboardEventKeycode))
    let special = [36:"Enter",48:"Tab",51:"Backspace",53:"Escape",123:"ArrowLeft",124:"ArrowRight",125:"ArrowDown",126:"ArrowUp",117:"Delete",115:"Home",119:"End",116:"PageUp",121:"PageDown",55:"Meta",54:"Meta",56:"Shift",60:"Shift",58:"Alt",61:"Alt",59:"Ctrl",62:"Ctrl",57:"CapsLock"]
    var length = 0
    raw.keyboardGetUnicodeString(maxStringLength: 0, actualStringLength: &length, unicodeString: nil)
    var units = [UniChar](repeating: 0, count: max(1, length))
    raw.keyboardGetUnicodeString(maxStringLength: units.count, actualStringLength: &length, unicodeString: &units)
    let text = String(decoding: units.prefix(length), as: UTF16.self)
    var fields: [String: Any] = ["keycode": code, "key": special[code] ?? (text.isEmpty ? "KeyCode:\(code)" : text)]
    if !text.isEmpty { fields["text"] = text }
    return fields
}
