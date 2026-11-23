package doc

import (
	"encoding/json"
	"sync"

	"golang.org/x/sync/singleflight"
	"lazymind/core/common/orm"
	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
)

type guideObservation struct {
	state         paperguide.SourceState
	precisionPath string
}

type guideObservationEntry struct {
	key, precisionStamp string
	guideObservation
	bytes int
}

var guideObservations struct {
	sync.Mutex
	entries []guideObservationEntry
	bytes   int
	loads   singleflight.Group
}

// The caller has just reauthorized the owner and hashed the original PDF.
// Track all candidate artifacts, including currently absent preferred ones,
// so a new VLM archive or changed job result invalidates the projection.
func guideMaterialKey(scope paperguide.Scope, source, hash string, jobs []orm.AsyncJob) string {
	type dependency struct{ Path, Stamp string }
	var files []dependency
	var paths []string
	for _, model := range []string{"vlm", "pipeline"} {
		paths = append(paths, sharedArchivePaths(source, pdfir.CachePath(source, hash, model))...)
	}
	for _, job := range jobs {
		var payload pdfIRPayload
		if job.Status != "succeeded" || json.Unmarshal(job.PayloadJSON, &payload) != nil || payload.SourceSHA256 != hash || payload.Revision != pdfir.Revision {
			continue
		}
		var result pdfIRResult
		if json.Unmarshal(job.ResultJSON, &result) == nil {
			paths = append(paths, result.IRPath, result.RawPath)
		}
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		stamp, err := guideFileIdentity(path)
		if err != nil {
			return ""
		}
		files = append(files, dependency{path, stamp})
	}
	raw, err := json.Marshal([]any{scope.Key(), source, hash, jobs, files})
	if err != nil {
		return ""
	}
	return pdfir.Digest(string(raw))
}

func observedGuideMaterial(scope paperguide.Scope, source, hash string, jobs []orm.AsyncJob, load func() (guideObservation, error)) (paperguide.SourceState, error) {
	key := guideMaterialKey(scope, source, hash, jobs)
	if key == "" {
		obs, err := load()
		return obs.state, err
	}
	value, err, _ := guideObservations.loads.Do(key, func() (any, error) {
		guideObservations.Lock()
		var cached guideObservationEntry
		for _, entry := range guideObservations.entries {
			if entry.key == key {
				cached = entry
				break
			}
		}
		guideObservations.Unlock()
		if cached.key != "" {
			if current, err := guideFileIdentity(cached.precisionPath); err == nil && current == cached.precisionStamp {
				return cached.state, nil
			}
		}
		obs, err := load()
		if err != nil || obs.state.Snapshot == nil || guideMaterialKey(scope, source, hash, jobs) != key {
			return obs.state, err
		}
		stamp, err := guideFileIdentity(obs.precisionPath)
		if err != nil {
			return obs.state, nil
		}
		if stamp != "missing" {
			// Bind the cached filesystem observation to the exact projection bytes
			// checked by the loader; a concurrent replacement cannot seed the cache.
			digest, err := pdfir.SourceHash(obs.precisionPath)
			after, e := guideFileIdentity(obs.precisionPath)
			if err != nil || e != nil || after != stamp || digest != obs.state.Snapshot.IRDigest {
				return obs.state, nil
			}
		}
		const maxBytes = maxGuideSnapshotBytes
		data, err := json.Marshal(obs.state.Snapshot)
		if err != nil || len(data) > maxBytes {
			return obs.state, nil
		}
		guideObservations.Lock()
		defer guideObservations.Unlock()
		// Bound both the number of documents and their serialized payload size.
		for i, old := range guideObservations.entries {
			if old.key == key {
				guideObservations.entries = append(guideObservations.entries[:i], guideObservations.entries[i+1:]...)
				guideObservations.bytes -= old.bytes
				break
			}
		}
		for len(guideObservations.entries) >= 16 || guideObservations.bytes+len(data) > maxBytes {
			guideObservations.bytes -= guideObservations.entries[0].bytes
			guideObservations.entries[0] = guideObservationEntry{}
			guideObservations.entries = guideObservations.entries[1:]
		}
		guideObservations.entries = append(guideObservations.entries, guideObservationEntry{key, stamp, obs, len(data)})
		guideObservations.bytes += len(data)
		return obs.state, nil
	})
	if err != nil {
		return paperguide.SourceState{}, err
	}
	return value.(paperguide.SourceState), nil
}
