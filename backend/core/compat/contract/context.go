package contract

// CallContext carries caller metadata for one operation.
type CallContext struct {
	UserID    string // Required; used for owner/ACL data scoping.
	UserName  string // Optional; mainly for audit fields.
	RequestID string // Optional; used for logs and tracing.
}
