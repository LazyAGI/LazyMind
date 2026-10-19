package codexruntime

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func currentSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid, nil
}
func platformProcess(pid int) (*ProcessInfo, error) {
	if pid < 1 {
		return nil, errors.New("invalid PID")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if err = windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return nil, err
	}
	var created, exited, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return nil, err
	}
	var token windows.Token
	if err = windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	owner, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid, err := currentSID()
	if err != nil {
		return nil, err
	}
	if !owner.User.Sid.Equals(sid) {
		return nil, errors.New("process belongs to another user")
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	parent := 0
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == uint32(pid) {
			parent = int(entry.ParentProcessID)
			break
		}
	}
	if parent == 0 {
		return nil, nil
	}
	executable := windows.UTF16ToString(buf[:size])
	return &ProcessInfo{pid, parent, executable, fmt.Sprintf("%d:%d:%s:%s", pid, created.Nanoseconds(), sid.String(), strings.ToLower(executable))}, nil
}
func platformRunning(executable string) (bool, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(executable)) {
			continue
		}
		p, e := platformProcess(int(entry.ProcessID))
		if e != nil {
			return false, e
		}
		if e == nil && p != nil && strings.EqualFold(p.Executable, executable) {
			return true, nil
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return false, err
	}
	return false, nil
}
func checkPrivate(file string) error {
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private path must not be a link")
	}
	sd, err := windows.GetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	sid, err := currentSID()
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if owner == nil || !owner.Equals(sid) {
		return errors.New("path belongs to another user")
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if acl == nil {
		return errors.New("private path has unrestricted ACL")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("unsupported private ACL entry")
		}
		who := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !who.Equals(sid) && who.String() != "S-1-5-18" && who.String() != "S-1-5-32-544" {
			return errors.New("private path grants access to other users")
		}
	}
	return nil
}
func makePrivate(file string) error {
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing ACL change on link")
	}
	sid, err := currentSID()
	if err != nil {
		return err
	}
	original, err := windows.GetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := original.Owner()
	if err != nil {
		return err
	}
	if !canPrivatizeOwner(owner, sid, windows.GetCurrentProcessToken().IsElevated()) {
		return errors.New("refusing ACL change on unowned path")
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	securityInfo := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	var newOwner *windows.SID
	if !owner.Equals(sid) {
		// Elevated Windows processes can create Administrators-owned paths.
		// Normalize them to the user SID; readers still require individual ownership.
		securityInfo |= windows.OWNER_SECURITY_INFORMATION
		newOwner = sid
	}
	return windows.SetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, securityInfo, newOwner, nil, acl, nil)
}

func canPrivatizeOwner(owner, user *windows.SID, elevated bool) bool {
	return owner != nil && (owner.Equals(user) || elevated && owner.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}
func detachedCommand(cmd *exec.Cmd) {
	// Failure to escape a restrictive Job is an explicit startup failure, never a
	// silent fallback to a backend that dies together with Desktop.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB, HideWindow: true}
}

var startupKeys = []string{"CODEX_CLI_PATH", "CODEX_APP_SERVER_FORCE_CLI", "CODEX_APP_SERVER_WS_URL", "CODEX_APP_SERVER_USE_LOCAL_DAEMON"}

func platformEnvironment(values map[string]*string, types map[string]uint32) (*environmentSnapshot, error) {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	if values != nil {
		if len(values) != len(startupKeys) {
			return nil, errors.New("invalid startup environment")
		}
		for _, name := range startupKeys {
			value, ok := values[name]
			if !ok {
				return nil, errors.New("missing startup variable")
			}
			if value == nil {
				err = key.DeleteValue(name)
				if errors.Is(err, registry.ErrNotExist) {
					err = nil
				}
			} else {
				if types[name] == registry.EXPAND_SZ {
					err = key.SetExpandStringValue(name, *value)
				} else {
					err = key.SetStringValue(name, *value)
				}
			}
			if err != nil {
				return nil, err
			}
		}
		text, _ := windows.UTF16PtrFromString("Environment")
		var result uintptr
		proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
		// A hung application must not block installation. Explorer may require a new
		// login if it does not consume this notification; never restart it ourselves.
		proc.Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(text)), 0x0002, 2000, uintptr(unsafe.Pointer(&result)))
	}
	result := &environmentSnapshot{Values: map[string]*string{}, Types: map[string]uint32{}}
	for _, name := range startupKeys {
		value, kind, err := key.GetStringValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			result.Values[name] = nil
		} else if err != nil {
			return nil, err
		} else {
			v := value
			result.Values[name] = &v
			result.Types[name] = kind
		}
	}
	return result, nil
}
