package desktopupdate

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func processStamp(pid int) (uint64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return 0, fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) <= 19 {
		return 0, fmt.Errorf("short process stat")
	}
	if fields[0] == "Z" {
		return 0, nil
	}
	return strconv.ParseUint(fields[19], 10, 64)
}
