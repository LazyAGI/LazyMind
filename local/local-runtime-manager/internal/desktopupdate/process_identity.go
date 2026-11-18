package desktopupdate

import "os"

func CurrentProcessIdentity() (ProcessIdentity, error) { return IdentifyProcess(os.Getpid()) }

func IdentifyProcess(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, failure("process_identity", nil)
	}
	stamp, err := processStamp(pid)
	if err != nil {
		return ProcessIdentity{}, failure("process_identity", err)
	}
	if stamp == 0 {
		return ProcessIdentity{}, failure("process_missing", nil)
	}
	return ProcessIdentity{PID: pid, StartID: stamp}, nil
}

func SameProcess(actor ProcessIdentity) (bool, error) {
	if actor.PID <= 0 || actor.StartID == 0 {
		return false, failure("process_identity", nil)
	}
	stamp, err := processStamp(actor.PID)
	if err != nil {
		return false, failure("process_identity", err)
	}
	return stamp == actor.StartID, nil
}
