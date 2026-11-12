package main

import "testing"

func TestScanWithoutRAGNormalPlanIncludesCollectors(t *testing.T) {
	cfg := RuntimeConfig{Algorithm: AlgorithmConfig{RAGDisabled: true}}
	plan := buildRuntimeProcessPlan(cfg)
	for _, name := range []string{scanControlPlaneProcessName, fileWatcherProcessName} {
		t.Run(name, func(t *testing.T) {
			if !plan.includes(name) {
				t.Fatalf("normal runtime must start %s without the optional RAG component", name)
			}
		})
	}
	for _, name := range []string{milvusLiteProcessName, processorServerProcessName, processorWorkerProcessName, algoProcessName, docServerProcessName} {
		if plan.includes(name) {
			t.Errorf("starting collectors must not enable optional RAG service %s", name)
		}
	}
}

func TestScanWithoutRAGWarmupStillSkipsCollectors(t *testing.T) {
	for _, ragDisabled := range []bool{false, true} {
		cfg := RuntimeConfig{MaintenanceMode: installerWarmupMaintenanceMode, Algorithm: AlgorithmConfig{RAGDisabled: ragDisabled}}
		plan := buildRuntimeProcessPlan(cfg)
		for _, name := range []string{scanControlPlaneProcessName, fileWatcherProcessName} {
			if plan.includes(name) {
				t.Errorf("installer warmup must not start %s (RAG disabled=%t)", name, ragDisabled)
			}
		}
	}
}
