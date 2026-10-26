// Package knowledgeruntime binds internal MCP dispatch to live executions and
// user decisions. Public MCP does not depend on this interaction protocol.
package knowledgeruntime

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/capability"
	"lazymind/core/doc"
	"lazymind/core/localworkspace"
	"lazymind/core/state"
	"time"
)

type Execution = localworkspace.OperationRequest
type Identity struct {
	Principal                       capability.Principal
	AllowedTools, AllowedDatasetIDs []string
}
type ValidateFunc func(context.Context, Execution) (Identity, error)
type ValidateCommitFunc func(context.Context, *gorm.DB, Execution) (Identity, error)
type SnapshotFunc func(context.Context, Identity, string, json.RawMessage) (doc.ResourceSnapshot, error)
type Runtime struct {
	state        state.Store
	validate     ValidateFunc
	snapshot     SnapshotFunc
	receipt      func(context.Context, Identity, PrepareRequest) (bool, error)
	secret       []byte
	now          func() time.Time
	pendingHook  func(context.Context, Execution, Prepared) error
	decisionHook func(context.Context, Execution, Prepared, string) error
}

func New(st state.Store, validate ValidateFunc, snapshot SnapshotFunc) (*Runtime, error) {
	if st == nil || validate == nil || snapshot == nil {
		return nil, errors.New("knowledge runtime dependencies missing")
	}
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		return nil, e
	}
	return &Runtime{state: st, validate: validate, snapshot: snapshot, secret: key, now: time.Now}, nil
}

const ttl = 5 * time.Minute

type PrepareRequest struct {
	Execution       Execution       `json:"execution"`
	WireName        string          `json:"wire_name"`
	Arguments       json.RawMessage `json:"arguments"`
	CallID          string          `json:"call_id"`
	OperationKey    string          `json:"operation_key,omitempty"`
	ArgumentsDigest string          `json:"arguments_digest,omitempty"`
	Snapshot        string          `json:"snapshot,omitempty"`
	ConfirmationID  string          `json:"confirmation_id,omitempty"`
}
type Prepared struct {
	OperationKey         string         `json:"operation_key"`
	ArgumentsDigest      string         `json:"arguments_digest"`
	Snapshot             string         `json:"snapshot"`
	RequiresConfirmation bool           `json:"requires_confirmation"`
	ConfirmationID       string         `json:"confirmation_id"`
	Status               string         `json:"status"`
	Preview              map[string]any `json:"preview"`
}
type pending struct {
	Request          PrepareRequest
	Prepared         Prepared
	Expires          int64
	UserID, TenantID string
}
type tokenClaims struct {
	Execution        Execution `json:"execution"`
	Expires          int64     `json:"exp"`
	Scope            string    `json:"scope"`
	UserID, TenantID string
	ConfirmationID   string `json:"confirmation_id,omitempty"`
}

func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (r *Runtime) sign(c tokenClaims) (string, error) {
	b, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, r.secret)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (r *Runtime) verify(token, scope string) (tokenClaims, error) {
	var c tokenClaims
	var p, s string
	for i, v := range token {
		if v == '.' {
			p, s = token[:i], token[i+1:]
			break
		}
	}
	raw, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || p == "" {
		return c, errors.New("invalid credential")
	}
	mac := hmac.New(sha256.New, r.secret)
	mac.Write([]byte(p))
	if !hmac.Equal(raw, mac.Sum(nil)) {
		return c, errors.New("invalid credential")
	}
	raw, e = base64.RawURLEncoding.DecodeString(p)
	if e != nil || json.Unmarshal(raw, &c) != nil || c.Scope != scope || c.Expires <= r.now().Unix() {
		return c, errors.New("expired or invalid credential")
	}
	return c, nil
}
func (r *Runtime) Credential(ctx context.Context, e Execution) (string, int64, error) {
	id, err := r.validate(ctx, e)
	if err != nil {
		return "", 0, err
	}
	expires := r.now().Add(ttl).Unix()
	token, err := r.sign(tokenClaims{Execution: e, Expires: expires, Scope: "knowledge-management", UserID: id.Principal.UserID, TenantID: id.Principal.TenantID})
	return token, expires, err
}
func (r *Runtime) Authenticate(ctx context.Context, token string) (Identity, Execution, error) {
	c, err := r.verify(token, "knowledge-management")
	if err != nil {
		return Identity{}, Execution{}, err
	}
	id, err := r.validate(ctx, c.Execution)
	if err == nil && (id.Principal.UserID != c.UserID || id.Principal.TenantID != c.TenantID) {
		err = errors.New("identity changed")
	}
	return id, c.Execution, err
}
func toolAllowed(id Identity, name string) bool {
	for _, t := range id.AllowedTools {
		if t == name {
			return true
		}
	}
	return false
}
func operationRefs(name string, args json.RawMessage) ([]doc.ResourceRef, bool, error) {
	switch name {
	case "knowledge.create", "knowledge.folder.create":
		return nil, true, nil
	case "knowledge.resource.delete":
		var in doc.DeleteResourcesRequest
		if e := json.Unmarshal(args, &in); e != nil {
			return nil, false, e
		}
		if len(in.Targets) == 0 {
			return nil, false, errors.New("empty targets")
		}
		return in.Targets, true, nil
	case "knowledge.resource.move":
		var in doc.MoveResourcesRequest
		if e := json.Unmarshal(args, &in); e != nil {
			return nil, false, e
		}
		if len(in.Sources) == 0 {
			return nil, false, errors.New("empty sources")
		}
		risk := false
		for _, s := range in.Sources {
			risk = risk || s.KnowledgeBaseID != in.Destination.KnowledgeBaseID
		}
		return append(in.Sources, in.Destination), risk, nil
	default:
		return nil, false, nil
	}
}
func canonicalArgs(raw json.RawMessage) (json.RawMessage, error) {
	var v map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e := decoder.Decode(&v); e != nil || v == nil {
		return nil, errors.New("arguments must be an object")
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(out.Bytes()), nil
}
func (r *Runtime) Prepare(ctx context.Context, req PrepareRequest) (Prepared, error) {
	if readOnlyTool(req.WireName) {
		return Prepared{}, errors.New("read-only tools do not create operations")
	}
	id, err := r.validate(ctx, req.Execution)
	if err != nil {
		return Prepared{}, err
	}
	if !toolAllowed(id, req.WireName) || req.CallID == "" {
		return Prepared{}, errors.New("tool not allowed or missing call ID")
	}
	args, err := canonicalArgs(req.Arguments)
	if err != nil {
		return Prepared{}, err
	}
	req.Arguments = args
	argsDigest := argumentsDigest(args)
	key := digest(struct {
		User, Tenant string
		Execution    Execution
		Call, Tool   string
	}{id.Principal.UserID, id.Principal.TenantID, req.Execution, req.CallID, req.WireName})
	if existing, err := r.load(ctx, key); err == nil {
		if existing.Prepared.ArgumentsDigest != argsDigest {
			return Prepared{}, errors.New("call ID reused with different arguments")
		}
		return r.Status(ctx, req.Execution, key)
	} else if !state.IsMissing(err) {
		return Prepared{}, err
	}
	refs, risk, err := operationRefs(req.WireName, args)
	if err != nil {
		return Prepared{}, err
	}
	snapshot := doc.ResourceSnapshot{Digest: argsDigest}
	if len(refs) > 0 || risk {
		snapshot, err = r.snapshot(ctx, id, req.WireName, args)
		if err != nil {
			return Prepared{}, err
		}
	}
	status := "allowed"
	if risk {
		status = "pending"
	}
	title, description, impact := req.WireName, "请核对选定资源、完整路径和数量。", ""
	switch req.WireName {
	case "knowledge.create":
		title = "确认创建知识库"
	case "knowledge.folder.create":
		title = "确认创建文件夹"
	case "knowledge.resource.delete":
		title = "确认删除知识库资源"
		impact = "删除后不可恢复。所选资源及其全部后代文档将立即不可见，文档处理状态和文档标签关联被清除；原件、解析数据、切片和索引随后清理，不影响其他资源上的同名标签。历史任务记录保留，被删资源不能继续读取，活动解析不得重新发布该资源；整库删除还会移除默认库及评测集绑定。当前部署没有撤销或恢复入口。"
	case "knowledge.resource.move":
		title = "确认跨知识库移动"
		impact = "每个独立根目录分别提交。成功项的源资源将不可见，目标获得新 ID 并按目标策略处理；未成功项保留源资源。"
	}
	out := Prepared{OperationKey: key, ArgumentsDigest: argsDigest, Snapshot: snapshot.Digest, RequiresConfirmation: risk, ConfirmationID: key, Status: status, Preview: map[string]any{"title": title, "description": description, "impact": impact, "resources": snapshot.PreviewResources, "destination": snapshot.Destination, "document_count": snapshot.DocumentCount, "folder_count": snapshot.FolderCount, "total_bytes": snapshot.TotalBytes}}
	if snapshot.Creation != nil {
		out.Preview["creation"] = snapshot.Creation
	}
	saved := pending{Request: req, Prepared: out, Expires: r.now().Add(ttl).Unix(), UserID: id.Principal.UserID, TenantID: id.Principal.TenantID}
	raw, _ := json.Marshal(saved)
	created, err := r.state.SetNX(ctx, "knowledge/prepare/"+key, raw, ttl)
	if err != nil {
		return Prepared{}, err
	}
	if !created {
		return r.Prepare(ctx, req)
	}
	return out, nil
}
func (r *Runtime) load(ctx context.Context, id string) (pending, error) {
	var p pending
	raw, e := r.state.Get(ctx, "knowledge/prepare/"+id)
	if e != nil {
		return p, e
	}
	e = json.Unmarshal(raw, &p)
	if e == nil && p.Expires <= r.now().Unix() {
		e = errors.New("confirmation expired")
	}
	return p, e
}
func (r *Runtime) Status(ctx context.Context, e Execution, id string) (Prepared, error) {
	identity, err := r.validate(ctx, e)
	if err != nil {
		return Prepared{}, err
	}
	return r.statusForIdentity(ctx, e, id, identity)
}

func (r *Runtime) statusForIdentity(ctx context.Context, e Execution, id string, identity Identity) (Prepared, error) {
	p, err := r.load(ctx, id)
	if err != nil {
		return Prepared{}, err
	}
	if digest(e) != digest(p.Request.Execution) || identity.Principal.UserID != p.UserID || identity.Principal.TenantID != p.TenantID {
		return Prepared{}, errors.New("confirmation belongs to another execution")
	}
	if !toolAllowed(identity, p.Request.WireName) {
		return Prepared{}, errors.New("tool no longer allowed")
	}
	out := p.Prepared
	if out.RequiresConfirmation {
		decision, err := r.state.Get(ctx, "knowledge/decision/"+id)
		if err != nil && !state.IsMissing(err) {
			return Prepared{}, err
		}
		if string(decision) == "approved" {
			out.Status = "allowed"
		} else if string(decision) == "rejected" {
			out.Status = "rejected"
		}
	}
	return out, nil
}
func (r *Runtime) Decide(ctx context.Context, user, tenant, id, decision, argsDigest, snapshot string) error {
	if decision != "approved" && decision != "rejected" {
		return errors.New("invalid decision")
	}
	p, err := r.load(ctx, id)
	if err != nil {
		return err
	}
	if p.UserID != user || p.TenantID != tenant || p.Prepared.ArgumentsDigest != argsDigest || p.Prepared.Snapshot != snapshot {
		return errors.New("decision identity or snapshot mismatch")
	}
	if !p.Prepared.RequiresConfirmation {
		return errors.New("operation does not require confirmation")
	}
	previous, readErr := r.state.Get(ctx, "knowledge/decision/"+id)
	if readErr != nil && !state.IsMissing(readErr) {
		return readErr
	}
	if readErr == nil {
		if string(previous) != decision {
			return errors.New("decision already made")
		}
	} else {
		if _, err = r.validate(ctx, p.Request.Execution); err != nil {
			return err
		}
		won, err := r.state.SetNX(ctx, "knowledge/decision/"+id, []byte(decision), time.Until(time.Unix(p.Expires, 0)))
		if err != nil {
			return err
		}
		if !won {
			old, err := r.state.Get(ctx, "knowledge/decision/"+id)
			if err != nil {
				return err
			}
			if string(old) != decision {
				return errors.New("decision already made")
			}
		}
	}
	// A recorded decision is immutable. Retry its history projection even if
	// the operation has completed since the first response was lost.
	if r.decisionHook != nil {
		return r.decisionHook(ctx, p.Request.Execution, p.Prepared, decision)
	}
	return nil
}
func readOnlyTool(name string) bool {
	return name == "knowledge.list" || name == "knowledge.document.list" || name == "knowledge.document.aggregate"
}

// authorizeOperation checks a write without issuing another credential.
func (r *Runtime) authorizeOperation(ctx context.Context, req PrepareRequest, identity Identity) (pending, Prepared, error) {
	p, err := r.load(ctx, req.ConfirmationID)
	if err != nil {
		return pending{}, Prepared{}, err
	}
	out, err := r.statusForIdentity(ctx, req.Execution, req.ConfirmationID, identity)
	if err != nil {
		return pending{}, Prepared{}, err
	}
	args, err := canonicalArgs(req.Arguments)
	if err != nil {
		return pending{}, Prepared{}, err
	}
	if out.Status != "allowed" || req.OperationKey != out.OperationKey || req.ArgumentsDigest != out.ArgumentsDigest || argumentsDigest(args) != out.ArgumentsDigest || req.Snapshot != out.Snapshot || req.CallID != p.Request.CallID || req.WireName != p.Request.WireName {
		return pending{}, Prepared{}, errors.New("operation is not authorized")
	}
	refs, risk, err := operationRefs(req.WireName, args)
	if err != nil {
		return pending{}, Prepared{}, err
	}
	committed := false
	if r.receipt != nil {
		committed, err = r.receipt(ctx, identity, req)
		if err != nil {
			return pending{}, Prepared{}, err
		}
	}
	if (len(refs) > 0 || risk) && !committed {
		snapshot, e := r.snapshot(ctx, identity, req.WireName, args)
		if e != nil {
			return pending{}, Prepared{}, e
		}
		if snapshot.Digest != out.Snapshot {
			return pending{}, Prepared{}, errors.New("resources changed; prepare and confirm again")
		}
	}
	return p, out, nil
}

func (r *Runtime) Claim(ctx context.Context, req PrepareRequest) (map[string]any, error) {
	identity, err := r.validate(ctx, req.Execution)
	if err != nil {
		return nil, err
	}
	p, out, err := r.authorizeOperation(ctx, req, identity)
	if err != nil {
		return nil, err
	}
	token, err := r.sign(tokenClaims{Execution: req.Execution, Expires: p.Expires, Scope: "knowledge-dispatch", UserID: p.UserID, TenantID: p.TenantID, ConfirmationID: req.ConfirmationID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"operation_key": out.OperationKey, "arguments_digest": out.ArgumentsDigest, "snapshot": out.Snapshot, "dispatch_token": token}, nil
}

func (r *Runtime) AuthorizeDispatch(ctx context.Context, execution Execution, name string, args json.RawMessage, metadata map[string]any, verified ...Identity) (capability.InvocationContext, error) {
	var id Identity
	var err error
	if len(verified) == 1 {
		id = verified[0]
	} else {
		id, err = r.validate(ctx, execution)
	}
	if err != nil {
		return capability.InvocationContext{}, err
	}
	if !toolAllowed(id, name) {
		return capability.InvocationContext{}, errors.New("tool not allowed")
	}
	if readOnlyTool(name) {
		return capability.InvocationContext{Principal: id.Principal, AllowedDatasetIDs: id.AllowedDatasetIDs}, nil
	}
	token, _ := metadata["dispatch_token"].(string)
	claims, err := r.verify(token, "knowledge-dispatch")
	if err != nil {
		return capability.InvocationContext{}, err
	}
	if digest(execution) != digest(claims.Execution) || claims.UserID != id.Principal.UserID || claims.TenantID != id.Principal.TenantID {
		return capability.InvocationContext{}, errors.New("dispatch execution mismatch")
	}
	p, err := r.load(ctx, claims.ConfirmationID)
	if err != nil {
		return capability.InvocationContext{}, err
	}
	req := p.Request
	req.ConfirmationID = claims.ConfirmationID
	req.OperationKey, req.ArgumentsDigest, req.Snapshot = p.Prepared.OperationKey, p.Prepared.ArgumentsDigest, p.Prepared.Snapshot
	req.Arguments, req.WireName = args, name
	if _, _, err = r.authorizeOperation(ctx, req, id); err != nil {
		return capability.InvocationContext{}, err
	}
	return capability.InvocationContext{ExecutionKey: claims.ConfirmationID, Principal: id.Principal, OperationKey: p.Prepared.OperationKey, ExpectedSnapshot: p.Prepared.Snapshot, AllowedDatasetIDs: id.AllowedDatasetIDs}, nil
}

func argumentsDigest(args []byte) string {
	sum := sha256.Sum256(args)
	return hex.EncodeToString(sum[:])
}

func (r *Runtime) SetPendingHook(fn func(context.Context, Execution, Prepared) error) {
	r.pendingHook = fn
}

// GuardCommit holds the owning execution rows until the resource transaction commits.
// The callback must query this transaction, not a separate connection.
func (r *Runtime) GuardCommit(ctx context.Context, tx *gorm.DB, key string, validate ValidateCommitFunc) error {
	p, err := r.load(ctx, key)
	if err != nil {
		return err
	}
	if validate == nil {
		return errors.New("knowledge execution guard unavailable")
	}
	if err = localworkspace.LockOperationRun(tx, p.Request.Execution); err != nil {
		return err
	}
	id, err := validate(ctx, tx, p.Request.Execution)
	if err != nil {
		return err
	}
	if !toolAllowed(id, p.Request.WireName) {
		return errors.New("knowledge execution is inactive or outside allowed scope")
	}
	return nil
}

// SetReceiptLookup lets a committed operation recover its original IDs even
// when its source resources no longer exist. Identity and arguments stay checked.
func (r *Runtime) SetReceiptLookup(fn func(context.Context, Identity, PrepareRequest) (bool, error)) {
	r.receipt = fn
}

// ValidateCommitPreparation refreshes external account policy after potentially
// slow file preparation, before opening the database transaction.
func (r *Runtime) ValidateCommitPreparation(ctx context.Context, c capability.InvocationContext) error {
	p, err := r.load(ctx, c.ExecutionKey)
	if err != nil {
		return err
	}
	id, err := r.validate(ctx, p.Request.Execution)
	if err != nil {
		return err
	}
	if id.Principal.UserID != c.Principal.UserID || id.Principal.TenantID != c.Principal.TenantID || !toolAllowed(id, p.Request.WireName) || (!id.Principal.Permissions.Has("document.write") && !id.Principal.Permissions.Has("*")) {
		return errors.New("knowledge execution is inactive or outside allowed scope")
	}
	if digest(id.AllowedDatasetIDs) != digest(c.AllowedDatasetIDs) {
		return errors.New("knowledge execution is inactive or outside allowed scope")
	}
	return nil
}

func (r *Runtime) SetDecisionHook(fn func(context.Context, Execution, Prepared, string) error) {
	r.decisionHook = fn
}
