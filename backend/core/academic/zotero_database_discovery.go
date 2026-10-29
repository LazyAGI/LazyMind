package academic

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type zoteroDatabaseLocation struct {
	Dir       string
	Path      string
	LinkedDir string
}

// All paths originate in local deployment configuration or the owner's Zotero
// profile. The HTTP request intentionally has no file or directory parameter.
func discoverZoteroDatabase() (*zoteroDatabaseLocation, error) {
	dir := strings.TrimSpace(os.Getenv("LAZYMIND_ZOTERO_DATA_DIR"))
	linked := strings.TrimSpace(os.Getenv("LAZYMIND_ZOTERO_LINKED_ATTACHMENT_DIR"))
	if dir == "" {
		home := strings.TrimSpace(os.Getenv("LAZYMIND_HOST_HOME"))
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home == "" || !filepath.IsAbs(home) {
			return nil, zoteroDatabaseUnavailable()
		}
		profile, err := findZoteroProfile(home)
		if err != nil {
			return nil, err
		}
		if profile != "" {
			prefs, err := readZoteroPathPreferences(filepath.Join(profile, "prefs.js"))
			if err != nil {
				return nil, err
			}
			if prefs.UseDataDir {
				if prefs.DataDir == "" {
					return nil, zoteroDatabaseUnavailable()
				}
				dir = prefs.DataDir
			}
			if linked == "" {
				linked = prefs.LinkedDir
			}
			if dir == "" {
				legacy := filepath.Join(profile, "zotero")
				if info, err := os.Stat(filepath.Join(legacy, "zotero.sqlite")); err == nil && info.Mode().IsRegular() {
					dir = legacy
				}
			}
		}
		if dir == "" {
			dir = filepath.Join(home, "Zotero")
		}
	}
	if !filepath.IsAbs(dir) {
		return nil, zoteroDatabaseUnavailable()
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, zoteroDatabaseUnavailable()
	}
	dbPath := filepath.Join(resolved, "zotero.sqlite")
	info, err := os.Lstat(dbPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, zoteroDatabaseUnavailable()
	}
	return &zoteroDatabaseLocation{Dir: resolved, Path: dbPath, LinkedDir: linked}, nil
}

func zoteroDatabaseUnavailable() *zoteroError {
	return zoteroFailure(503, "ZOTERO_DATABASE_UNAVAILABLE", "未找到可读取的本机 Zotero 数据库；请检查 Zotero 数据目录，容器部署需挂载该目录")
}

func findZoteroProfile(home string) (string, error) {
	var root string
	switch runtime.GOOS {
	case "darwin":
		root = filepath.Join(home, "Library", "Application Support", "Zotero")
	case "windows":
		appData := strings.TrimSpace(os.Getenv("APPDATA"))
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		root = filepath.Join(appData, "Zotero", "Zotero")
	default:
		root = filepath.Join(home, ".zotero", "zotero")
	}
	data, err := readZoteroConfigFile(filepath.Join(root, "profiles.ini"), 1<<20)
	if os.IsNotExist(err) {
		// Older installations may lack profiles.ini. Only an unambiguous profile
		// is accepted; never choose whichever directory happens to sort first.
		patterns := []string{filepath.Join(root, "Profiles", "*", "prefs.js"), filepath.Join(root, "*", "prefs.js")}
		found := []string{}
		for _, pattern := range patterns {
			matches, _ := filepath.Glob(pattern)
			found = append(found, matches...)
		}
		if len(found) == 1 {
			return filepath.Dir(found[0]), nil
		}
		if len(found) > 1 {
			return "", zoteroAmbiguousProfile()
		}
		return "", nil
	}
	if err != nil {
		return "", zoteroDatabaseUnavailable()
	}
	type section struct {
		name   string
		values map[string]string
	}
	sections := []section{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sections = append(sections, section{line[1 : len(line)-1], map[string]string{}})
			continue
		}
		if len(sections) == 0 || line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			sections[len(sections)-1].values[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	if scanner.Err() != nil {
		return "", zoteroDatabaseUnavailable()
	}
	profiles := []string{}
	defaults := []string{}
	installs := []string{}
	for _, s := range sections {
		if strings.HasPrefix(s.name, "Install") && s.values["Default"] != "" {
			installs = append(installs, s.values["Default"])
			continue
		}
		if !strings.HasPrefix(s.name, "Profile") || s.values["Path"] == "" {
			continue
		}
		p := s.values["Path"]
		if s.values["IsRelative"] != "0" {
			p = filepath.Join(root, filepath.FromSlash(p))
		}
		if !filepath.IsAbs(p) {
			return "", zoteroDatabaseUnavailable()
		}
		profiles = append(profiles, p)
		if s.values["Default"] == "1" {
			defaults = append(defaults, p)
		}
	}
	// Modern profile managers choose a profile per installation. A legacy
	// Profile Default=1 can remain after that choice changes.
	installPaths := map[string]bool{}
	for _, p := range installs {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, filepath.FromSlash(p))
		}
		installPaths[filepath.Clean(p)] = true
	}
	if len(installPaths) > 1 {
		return "", zoteroAmbiguousProfile()
	}
	for p := range installPaths {
		for _, candidate := range profiles {
			if filepath.Clean(candidate) == filepath.Clean(p) {
				return candidate, nil
			}
		}
		return "", zoteroAmbiguousProfile()
	}
	if len(defaults) == 1 {
		return defaults[0], nil
	}
	if len(defaults) > 1 {
		return "", zoteroAmbiguousProfile()
	}
	if len(profiles) == 1 {
		return profiles[0], nil
	}
	if len(profiles) > 1 {
		return "", zoteroAmbiguousProfile()
	}
	return "", nil
}

func zoteroAmbiguousProfile() *zoteroError {
	return zoteroFailure(409, "ZOTERO_DATABASE_PROFILE_AMBIGUOUS", "检测到多个 Zotero 配置且无法确定默认配置，请通过 LAZYMIND_ZOTERO_DATA_DIR 指定当前数据目录")
}

type zoteroPathPreferences struct {
	UseDataDir         bool
	DataDir, LinkedDir string
}

var zoteroPathPreference = regexp.MustCompile(`(?m)^\s*user_pref\("extensions\.zotero\.(useDataDir|dataDir|baseAttachmentPath)",\s*(.+)\);\s*$`)

func readZoteroPathPreferences(path string) (zoteroPathPreferences, error) {
	var prefs zoteroPathPreferences
	data, err := readZoteroConfigFile(path, 4<<20)
	if os.IsNotExist(err) {
		return prefs, nil
	}
	if err != nil {
		return prefs, zoteroDatabaseUnavailable()
	}
	// Parse only three JSON literals, never execute prefs.js or load credentials.
	for _, match := range zoteroPathPreference.FindAllSubmatch(data, -1) {
		switch string(match[1]) {
		case "useDataDir":
			err = json.Unmarshal(match[2], &prefs.UseDataDir)
		case "dataDir":
			err = json.Unmarshal(match[2], &prefs.DataDir)
		case "baseAttachmentPath":
			err = json.Unmarshal(match[2], &prefs.LinkedDir)
		}
		if err != nil {
			return prefs, zoteroDatabaseUnavailable()
		}
	}
	return prefs, nil
}

func readZoteroConfigFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, io.ErrShortBuffer
	}
	return b, nil
}
