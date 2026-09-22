import ApplicationServices

@main
struct KeyboardTests {
    static func main() {
        // Synthetic events are never posted to the system; no recording permissions are needed.
        for text in ["a", "A", "@", "你好😀", "test-secret-value"] {
            let event = CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: true)!
            let units = Array(text.utf16)
            event.keyboardSetUnicodeString(stringLength: units.count, unicodeString: units)
            let fields = recordingKeyboardFields(event)
            precondition(fields["key"] as? String == text)
            precondition(fields["text"] as? String == text)
            precondition(fields["keycode"] as? Int == 0)
        }
        let enter = CGEvent(keyboardEventSource: nil, virtualKey: 36, keyDown: true)!
        precondition(recordingKeyboardFields(enter)["key"] as? String == "Enter")
        print("Keyboard extraction tests passed")
    }
}
