package codexplugin

// DispatcherBundle is supervised by Assistant Bridge, independently of MCP sessions.
func DispatcherBundle() ([]byte, error) { return assets.ReadFile("assets/dispatcher.mjs") }
