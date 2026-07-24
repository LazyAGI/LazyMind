package main

// runtimeProcessPlan is the single source of truth for the services that a
// runtime mode starts, waits for, reports, and stops.
type runtimeProcessPlan struct {
	HostProcesses     []string
	AlgorithmServices []AlgorithmServiceSpec
}

func buildRuntimeProcessPlan(cfg RuntimeConfig) runtimeProcessPlan {
	plan := runtimeProcessPlan{
		HostProcesses: []string{
			localProxyProcessName,
			authServiceProcessName,
			coreProcessName,
			frontendProcessName,
		},
	}
	if cfg.MaintenanceMode != installerWarmupMaintenanceMode {
		plan.HostProcesses = append(plan.HostProcesses, scanControlPlaneProcessName, fileWatcherProcessName)
	}
	if cfg.ModeProfile.VectorStore.ManagedProcess {
		plan.HostProcesses = append(plan.HostProcesses, milvusLiteProcessName)
	}
	for _, spec := range algorithmProcessSpecs(cfg.Algorithm) {
		if cfg.MaintenanceMode == installerWarmupMaintenanceMode && spec.Name == processorWorkerProcessName {
			continue
		}
		plan.AlgorithmServices = append(plan.AlgorithmServices, spec)
	}
	return plan
}

func (p runtimeProcessPlan) serviceNames() []string {
	names := make([]string, 0, len(p.HostProcesses)+len(p.AlgorithmServices)+1)
	names = append(names, processComposeServiceName)
	names = append(names, p.HostProcesses...)
	for _, spec := range p.AlgorithmServices {
		names = append(names, spec.Name)
	}
	return names
}

func (p runtimeProcessPlan) includes(name string) bool {
	if name == processComposeServiceName {
		return true
	}
	for _, planned := range p.HostProcesses {
		if planned == name {
			return true
		}
	}
	for _, spec := range p.AlgorithmServices {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func (p runtimeProcessPlan) isAlgorithmProcess(name string) bool {
	for _, spec := range p.AlgorithmServices {
		if spec.Name == name {
			return true
		}
	}
	return false
}
