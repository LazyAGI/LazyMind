package doc

import (
	"context"
	"encoding/json"
	"errors"
	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/paperguide"
	"lazymind/core/store"
)

const PaperGuideJobType = paperguide.JobType

func paperGuideService() paperguide.Service {
	return paperguide.Service{FeatureEnabled: PaperGuideEnabled, Repo: paperguide.Repository{DB: store.DB(), PrepareCheckpoint: preparePaperGuideCheckpoint, CheckpointGuard: paperGuideCheckpointGuard}, Source: paperGuideSource{}, Authorize: authorizePaperGuide, ResolveModel: resolvePaperGuideModel, Call: func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
		result, e := algo.RunStructuredTask(ctx, algo.StructuredTaskRequest{
			Task: task, Version: paperguide.TaskVersion, Input: input, LLMConfig: cfg,
			TransportTimeout: paperguide.ModelAttemptTimeout(task),
		})
		if e != nil {
			// Preserve only safe cancellation causes, never raw provider errors.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(e, context.DeadlineExceeded) {
				return nil, context.DeadlineExceeded
			}
			if errors.Is(e, context.Canceled) {
				return nil, context.Canceled
			}
			class := "transport"
			var structured *algo.StructuredTaskError
			if errors.As(e, &structured) {
				switch structured.Code {
				case "request_timeout", "task_timeout":
					class = "timeout"
				case "invalid_model_output":
					class = "schema"
				case "rate_limited":
					class = "rate_limited"
				case "model_refusal":
					class = "refusal"
				case "context_length_exceeded":
					class = "context"
				}
			}
			return nil, &paperguide.CallError{Class: class}
		}
		return result.Output, nil
	}}
}
func RegisterPaperGuideJobs() {
	asyncjob.Register(PaperGuideJobType, func(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
		return paperGuideService().Run(ctx, job, reporter)
	})
}
