package crawl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const BindingStatusActive = "ACTIVE"

type BindingReader interface {
	GetBinding(ctx context.Context, sourceID, bindingID string) (store.Binding, error)
}

type SourceReader interface {
	GetSource(ctx context.Context, sourceID string) (store.Source, error)
}

type ObjectWriter interface {
	UpsertObjects(ctx context.Context, objects []store.SourceObject) error
}

type BindingErrorRecorder interface {
	MarkBindingError(ctx context.Context, sourceID, bindingID string, generation int64, lastError store.JSON, now time.Time) error
}

type StateReducer interface {
	ReduceSeenObjects(ctx context.Context, input ReduceSeenInput) (ReduceSeenResult, error)
	ReduceMissingObjects(ctx context.Context, input ReduceMissingInput) (ReduceMissingResult, error)
}

type DefaultCrawlEngine struct {
	bindings            BindingReader
	registry            connector.ConnectorRegistry
	objects             ObjectWriter
	states              StateReducer
	errors              BindingErrorRecorder
	clock               func() time.Time
	sleep               func(context.Context, time.Duration) error
	pageSize            int
	listRequestInterval time.Duration
	factory             CrawlStrategyFactory
}

type runContext struct {
	claim   BindingRunClaim
	binding store.Binding
	conn    connector.SourceConnector
	spec    connector.ConnectorSpec
}

type loopResult struct {
	Objects []store.SourceObject
	Keys    []string
}

type Option func(*DefaultCrawlEngine)

func NewDefaultCrawlEngine(bindings BindingReader, registry connector.ConnectorRegistry, objects ObjectWriter, states StateReducer, options ...Option) *DefaultCrawlEngine {
	e := &DefaultCrawlEngine{
		bindings: bindings,
		registry: registry,
		objects:  objects,
		states:   states,
		clock:    time.Now,
		sleep:    sleepContext,
		pageSize: 100,
		factory:  defaultStrategyFactory{},
	}
	if recorder, ok := bindings.(BindingErrorRecorder); ok {
		e.errors = recorder
	}
	for _, option := range options {
		option(e)
	}
	return e
}

func WithBindingErrorRecorder(recorder BindingErrorRecorder) Option {
	return func(e *DefaultCrawlEngine) {
		e.errors = recorder
	}
}

func WithClock(clock func() time.Time) Option {
	return func(e *DefaultCrawlEngine) {
		if clock != nil {
			e.clock = clock
		}
	}
}

func WithPageSize(pageSize int) Option {
	return func(e *DefaultCrawlEngine) {
		if pageSize > 0 {
			e.pageSize = pageSize
		}
	}
}

func WithListRequestInterval(interval time.Duration) Option {
	return func(e *DefaultCrawlEngine) {
		if interval >= 0 {
			e.listRequestInterval = interval
		}
	}
}

func withSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(e *DefaultCrawlEngine) {
		if sleep != nil {
			e.sleep = sleep
		}
	}
}

func WithStrategyFactory(factory CrawlStrategyFactory) Option {
	return func(e *DefaultCrawlEngine) {
		if factory != nil {
			e.factory = factory
		}
	}
}

func (e *DefaultCrawlEngine) Run(ctx context.Context, claim BindingRunClaim) (RunResult, error) {
	runCtx, result, ok, err := e.loadRunContext(ctx, claim)
	if err != nil || !ok {
		return result, err
	}
	strategy, err := e.selectStrategy(runCtx)
	if err != nil {
		return result, err
	}
	loop, strategy, err := e.crawlWithFallback(ctx, runCtx, strategy)
	if err != nil {
		e.recordBindingErrorIfNeeded(ctx, runCtx, err)
		return e.failedResult(result, strategy.Coverage(), err), nil
	}
	return e.finishCrawl(ctx, runCtx, result, strategy, loop)
}

func (e *DefaultCrawlEngine) loadRunContext(ctx context.Context, claim BindingRunClaim) (runContext, RunResult, bool, error) {
	result := RunResult{RunID: claim.RunID}
	binding, err := e.bindings.GetBinding(ctx, claim.SourceID, claim.BindingID)
	if err != nil {
		return runContext{}, result, false, err
	}
	if binding.BindingGeneration != claim.BindingGeneration {
		return runContext{}, canceledResult(result, claim.ScopeType), false, nil
	}
	if binding.Status != "" && binding.Status != BindingStatusActive {
		return runContext{}, inactiveBindingResult(result, claim.ScopeType), false, nil
	}
	if reader, ok := e.bindings.(SourceReader); ok {
		source, err := reader.GetSource(ctx, claim.SourceID)
		if err != nil {
			return runContext{}, result, false, err
		}
		binding.ProviderOptions = providerOptionsWithRunActor(binding.ProviderOptions, source.CreatedBy, source.TenantID)
	}
	conn, err := e.registry.Get(connector.ConnectorType(binding.ConnectorType))
	if err != nil {
		return runContext{}, result, false, err
	}
	return runContext{claim: claim, binding: binding, conn: conn, spec: conn.Spec()}, result, true, nil
}

func (e *DefaultCrawlEngine) selectStrategy(runCtx runContext) (CrawlStrategy, error) {
	return e.factory.Strategy(strategyInput{
		binding:  runCtx.binding,
		conn:     runCtx.conn,
		spec:     runCtx.spec,
		claim:    runCtx.claim,
		pageSize: e.pageSize,
	})
}

func (e *DefaultCrawlEngine) crawlWithFallback(ctx context.Context, runCtx runContext, strategy CrawlStrategy) (loopResult, CrawlStrategy, error) {
	loop, err := e.crawlLoop(ctx, runCtx, strategy)
	if isDeltaUnsupported(err) {
		runCtx.claim.ScopeType = connector.ScopeTypeFull
		runCtx.claim.Cursor = ""
		fallback, selectErr := e.selectStrategy(runCtx)
		if selectErr != nil {
			return loopResult{}, strategy, selectErr
		}
		loop, err = e.crawlLoop(ctx, runCtx, fallback)
		return loop, fallback, err
	}
	return loop, strategy, err
}

func (e *DefaultCrawlEngine) crawlLoop(ctx context.Context, runCtx runContext, strategy CrawlStrategy) (loopResult, error) {
	result := loopResult{}
	throttle := crawlListThrottle{interval: e.listRequestInterval, sleep: e.sleep}
	for {
		req, done, err := strategy.NextRequest(ctx, CrawlLoopState{})
		if err != nil || done {
			return result, err
		}
		page, err := e.fetchPage(ctx, runCtx.conn, req, &throttle)
		if err != nil {
			return result, err
		}
		objects, keys, err := e.normalizePage(ctx, runCtx, page)
		if err != nil {
			return result, err
		}
		result.Objects = append(result.Objects, objects...)
		result.Keys = append(result.Keys, keys...)
		if err := strategy.ObservePage(ctx, page); err != nil {
			return result, err
		}
	}
}

func (e *DefaultCrawlEngine) fetchPage(ctx context.Context, conn connector.SourceConnector, req CrawlRequest, throttle *crawlListThrottle) (connector.RawObjectPage, error) {
	switch req.Kind {
	case CrawlRequestKindFetch:
		return conn.FetchPage(ctx, req.Fetch)
	case CrawlRequestKindListChildren:
		if err := throttle.wait(ctx); err != nil {
			return connector.RawObjectPage{}, err
		}
		return conn.ListChildren(ctx, req.ListChildren)
	default:
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidArgument, "crawl request kind is unsupported")
	}
}

type crawlListThrottle struct {
	interval time.Duration
	sleep    func(context.Context, time.Duration) error
	seen     bool
}

func (t *crawlListThrottle) wait(ctx context.Context) error {
	if t == nil || t.interval <= 0 {
		return nil
	}
	if !t.seen {
		t.seen = true
		return nil
	}
	return t.sleep(ctx, t.interval)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (e *DefaultCrawlEngine) normalizePage(ctx context.Context, runCtx runContext, page connector.RawObjectPage) ([]store.SourceObject, []string, error) {
	objects := make([]store.SourceObject, 0, len(page.Items))
	keys := make([]string, 0, len(page.Items))
	for _, raw := range page.Items {
		object, err := e.normalizeObject(ctx, runCtx, raw)
		if err != nil {
			return nil, nil, err
		}
		objects = append(objects, object)
		keys = append(keys, object.ObjectKey)
	}
	return objects, keys, nil
}

func (e *DefaultCrawlEngine) normalizeObject(ctx context.Context, runCtx runContext, raw connector.RawObject) (store.SourceObject, error) {
	normalized, err := runCtx.conn.MapObject(ctx, raw)
	if err != nil {
		return store.SourceObject{}, err
	}
	return sourceObjectFromNormalized(runCtx.binding, normalized, runCtx.claim.RunID, e.clock())
}

func (e *DefaultCrawlEngine) finishCrawl(ctx context.Context, runCtx runContext, result RunResult, strategy CrawlStrategy, loop loopResult) (RunResult, error) {
	if stale, err := e.staleGenerationResult(ctx, runCtx, &result); err != nil || stale {
		return result, err
	}
	loop.Objects = e.objectsForRun(runCtx, loop.Objects)
	loop.Keys = objectKeys(loop.Objects)
	if err := e.writePageObjects(ctx, loop.Objects); err != nil {
		return result, err
	}
	if stale, err := e.staleGenerationResult(ctx, runCtx, &result); err != nil || stale {
		return result, err
	}
	seen, err := e.reduceSeenStates(ctx, runCtx, loop.Objects)
	if err != nil {
		return result, err
	}
	coverage := strategy.Coverage()
	missing, err := e.reduceMissingStates(ctx, runCtx, coverage, loop.Keys)
	if err != nil {
		return result, err
	}
	result.Status = RunStatusSucceeded
	result.Coverage = coverage
	result.NextCursor = strategy.NextCursor()
	result.Counts = buildCounts(loop.Objects, seen, missing)
	return result, nil
}

func (e *DefaultCrawlEngine) staleGenerationResult(ctx context.Context, runCtx runContext, result *RunResult) (bool, error) {
	current, err := e.bindings.GetBinding(ctx, runCtx.claim.SourceID, runCtx.claim.BindingID)
	if err != nil {
		return false, err
	}
	if current.BindingGeneration == runCtx.claim.BindingGeneration {
		return false, nil
	}
	*result = canceledResult(*result, runCtx.claim.ScopeType)
	return true, nil
}

func (e *DefaultCrawlEngine) objectsForRun(runCtx runContext, objects []store.SourceObject) []store.SourceObject {
	if runCtx.claim.ScopeType == connector.ScopeTypeFull && !hasObjectKey(objects, runCtx.binding.TreeKey) {
		objects = append([]store.SourceObject{rootObjectFromBinding(runCtx.binding, runCtx.claim.RunID, e.clock())}, objects...)
	}
	for i := range objects {
		objects[i] = normalizeIndexedObject(runCtx.binding, objects[i])
	}
	computeObjectDepths(objects)
	return objects
}

func hasObjectKey(objects []store.SourceObject, objectKey string) bool {
	if objectKey == "" {
		return false
	}
	for _, object := range objects {
		if object.ObjectKey == objectKey {
			return true
		}
	}
	return false
}

func objectKeys(objects []store.SourceObject) []string {
	keys := make([]string, 0, len(objects))
	for _, object := range objects {
		keys = append(keys, object.ObjectKey)
	}
	return keys
}

func (e *DefaultCrawlEngine) writePageObjects(ctx context.Context, objects []store.SourceObject) error {
	if len(objects) == 0 {
		return nil
	}
	return e.objects.UpsertObjects(ctx, objects)
}

func (e *DefaultCrawlEngine) reduceSeenStates(ctx context.Context, runCtx runContext, objects []store.SourceObject) (ReduceSeenResult, error) {
	return e.states.ReduceSeenObjects(ctx, ReduceSeenInput{
		SourceID:          runCtx.claim.SourceID,
		BindingID:         runCtx.claim.BindingID,
		BindingGeneration: runCtx.claim.BindingGeneration,
		RunID:             runCtx.claim.RunID,
		Objects:           objects,
		DetectedAt:        e.clock(),
	})
}

func (e *DefaultCrawlEngine) reduceMissingStates(ctx context.Context, runCtx runContext, coverage Coverage, seenKeys []string) (ReduceMissingResult, error) {
	return e.states.ReduceMissingObjects(ctx, ReduceMissingInput{
		SourceID:          runCtx.claim.SourceID,
		BindingID:         runCtx.claim.BindingID,
		BindingGeneration: runCtx.claim.BindingGeneration,
		RunID:             runCtx.claim.RunID,
		Coverage:          coverage,
		SeenObjectKeys:    seenKeys,
		RunSucceeded:      true,
		DetectedAt:        e.clock(),
	})
}

func (e *DefaultCrawlEngine) failedResult(result RunResult, coverage Coverage, err error) RunResult {
	result.Status = RunStatusFailed
	result.Coverage = coverage
	result.ErrorMessage = err.Error()
	if code, ok := connector.ErrorCodeOf(err); ok {
		result.ErrorCode = string(code)
	}
	return result
}

func (e *DefaultCrawlEngine) recordBindingErrorIfNeeded(ctx context.Context, runCtx runContext, err error) {
	if e.errors == nil {
		return
	}
	code, ok := connector.ErrorCodeOf(err)
	if !ok || code != connector.ErrorCodePermissionDenied {
		return
	}
	_ = e.errors.MarkBindingError(ctx, runCtx.claim.SourceID, runCtx.claim.BindingID, runCtx.claim.BindingGeneration, store.JSON{
		"code":    string(code),
		"message": err.Error(),
	}, e.clock().UTC())
}

func buildCounts(objects []store.SourceObject, seen ReduceSeenResult, missing ReduceMissingResult) Counts {
	return Counts{
		Seen:      int64(len(objects)),
		New:       seen.NewCount,
		Modified:  seen.ModifiedCount,
		Deleted:   seen.DeletedCount + missing.DeletedCount,
		Unchanged: seen.UnchangedCount,
	}
}

func canceledResult(result RunResult, scopeType connector.ScopeType) RunResult {
	result.Status = RunStatusCanceled
	result.ErrorCode = string(store.ErrCodeGenerationConflict)
	result.ErrorMessage = "binding generation changed"
	result.Coverage = Coverage{ScopeType: scopeType, Complete: false, ExcludedReason: "generation_conflict"}
	return result
}

func inactiveBindingResult(result RunResult, scopeType connector.ScopeType) RunResult {
	result.Status = RunStatusCanceled
	result.ErrorCode = "BINDING_INACTIVE"
	result.ErrorMessage = "binding is not active"
	result.Coverage = Coverage{ScopeType: scopeType, Complete: false, ExcludedReason: "binding_inactive"}
	return result
}

func isDeltaUnsupported(err error) bool {
	code, ok := connector.ErrorCodeOf(err)
	return ok && code == connector.ErrorCodeUnsupportedDelta
}

func sourceObjectFromNormalized(binding store.Binding, object connector.NormalizedSourceObject, runID string, now time.Time) (store.SourceObject, error) {
	if binding.SourceID == "" || binding.BindingID == "" {
		return store.SourceObject{}, fmt.Errorf("binding identity is required")
	}
	if object.ObjectKey == "" {
		return store.SourceObject{}, fmt.Errorf("object_key is required")
	}
	if strings.TrimSpace(object.DisplayName) == "" {
		return store.SourceObject{}, fmt.Errorf("display_name is required")
	}
	indexed := store.SourceObject{
		SourceID:        binding.SourceID,
		BindingID:       binding.BindingID,
		TreeKey:         binding.TreeKey,
		ObjectKey:       object.ObjectKey,
		ParentKey:       object.ParentKey,
		DisplayName:     object.DisplayName,
		SearchName:      object.SearchName,
		ObjectType:      string(object.ObjectType),
		IsDocument:      object.IsDocument,
		IsContainer:     object.IsContainer,
		HasChildren:     object.HasChildren,
		SourceVersion:   object.SourceVersion,
		SizeBytes:       object.SizeBytes,
		MimeType:        object.MimeType,
		FileExtension:   object.FileExtension,
		ModifiedAt:      object.ModifiedAt,
		DeletedAtSource: object.DeletedAtSource,
		Depth:           0,
		ProviderMeta:    providerMeta(object.ProviderMeta),
		LastSeenRunID:   runID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	return normalizeIndexedObject(binding, indexed), nil
}

func rootObjectFromBinding(binding store.Binding, runID string, now time.Time) store.SourceObject {
	displayName := strings.TrimSpace(binding.CoreParentDocumentName)
	if displayName == "" {
		displayName = strings.TrimSpace(binding.TargetRef)
	}
	if displayName == "" {
		displayName = binding.BindingID
	}
	return store.SourceObject{
		SourceID:      binding.SourceID,
		BindingID:     binding.BindingID,
		TreeKey:       binding.TreeKey,
		ObjectKey:     binding.TreeKey,
		DisplayName:   displayName,
		SearchName:    strings.ToLower(displayName),
		ObjectType:    "folder",
		IsContainer:   true,
		HasChildren:   true,
		Depth:         0,
		LastSeenRunID: runID,
		ProviderMeta: store.JSON{
			"binding_root": true,
			"target_type":  binding.TargetType,
			"target_ref":   binding.TargetRef,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func normalizeIndexedObject(binding store.Binding, object store.SourceObject) store.SourceObject {
	if object.SourceID == "" {
		object.SourceID = binding.SourceID
	}
	if object.BindingID == "" {
		object.BindingID = binding.BindingID
	}
	if object.TreeKey == "" {
		object.TreeKey = binding.TreeKey
	}
	if object.SearchName == "" {
		object.SearchName = strings.ToLower(object.DisplayName)
	}
	object.ParentKey = normalizeParentKey(binding, object)
	return object
}

func normalizeParentKey(binding store.Binding, object store.SourceObject) string {
	parent := strings.TrimSpace(object.ParentKey)
	if object.ObjectKey == binding.TreeKey {
		return ""
	}
	if parent == binding.TargetRef && binding.TreeKey != "" {
		return binding.TreeKey
	}
	if parent == "" {
		return ""
	}
	return parent
}

func computeObjectDepths(objects []store.SourceObject) {
	byKey := make(map[string]int, len(objects))
	for i, object := range objects {
		if object.ObjectKey != "" {
			byKey[object.ObjectKey] = i
		}
	}
	memo := make(map[string]int64, len(objects))
	visiting := make(map[string]struct{}, len(objects))
	var depthOf func(string) int64
	depthOf = func(objectKey string) int64 {
		if value, ok := memo[objectKey]; ok {
			return value
		}
		i, ok := byKey[objectKey]
		if !ok {
			return 0
		}
		object := objects[i]
		if object.ParentKey == "" {
			memo[objectKey] = 0
			return 0
		}
		if _, ok := visiting[objectKey]; ok {
			if object.Depth > 0 {
				return object.Depth
			}
			return 1
		}
		visiting[objectKey] = struct{}{}
		parentDepth := int64(0)
		if _, ok := byKey[object.ParentKey]; ok {
			parentDepth = depthOf(object.ParentKey)
		} else if object.Depth > 0 {
			parentDepth = object.Depth - 1
		}
		delete(visiting, objectKey)
		memo[objectKey] = parentDepth + 1
		return memo[objectKey]
	}
	for i := range objects {
		objects[i].Depth = depthOf(objects[i].ObjectKey)
	}
}

func providerOptions(in store.JSON) connector.ProviderOptions {
	if in == nil {
		return nil
	}
	out := make(connector.ProviderOptions, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func providerOptionsWithRunActor(options store.JSON, userID, tenantID string) store.JSON {
	out := store.CloneJSON(options)
	if out == nil {
		out = store.JSON{}
	}
	if userID != "" {
		out["user_id"] = userID
	}
	if tenantID != "" {
		out["tenant_id"] = tenantID
	}
	return out
}

func providerMeta(in connector.ProviderMeta) store.JSON {
	if in == nil {
		return nil
	}
	out := make(store.JSON, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
