package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const (
	overlayModeKey             = "mode"
	overlayDisabledServicesKey = "disabled_container_services"
)

type OverlayConfig struct {
	Mode                   string
	DisabledContainerTypes []string
}

func parseRuntimeOverlay(path string) (OverlayConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return OverlayConfig{}, err
	}
	defer f.Close()

	cfg := OverlayConfig{}
	var inOverlay bool
	var inDisabled bool
	var overlayIndent int
	var disabledIndent int

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		indent := leadingSpaces(raw)

		if !inOverlay {
			if strings.HasPrefix(line, "x-lazymind-local:") {
				inOverlay = true
				overlayIndent = indent
			}
			continue
		}

		if indent <= overlayIndent {
			// End of extension section.
			break
		}

		if inDisabled {
			if indent <= disabledIndent {
				inDisabled = false
			} else {
				if strings.HasPrefix(line, "- ") {
					cfg.DisabledContainerTypes = append(cfg.DisabledContainerTypes, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
					continue
				}
			}
		}

		if !inDisabled {
			if strings.HasPrefix(line, overlayModeKey+":") {
				cfg.Mode = strings.TrimSpace(strings.TrimPrefix(line, overlayModeKey+":"))
			}
			if strings.HasPrefix(line, overlayDisabledServicesKey+":") {
				inDisabled = true
				disabledIndent = indent
			}
		}
	}

	if err := sc.Err(); err != nil {
		return OverlayConfig{}, err
	}
	return cfg, nil
}

func leadingSpaces(line string) int {
	count := 0
	for _, r := range line {
		if r == ' ' {
			count++
			continue
		}
		break
	}
	return count
}

func parseServiceLines(output string) []string {
	parts := strings.Split(output, "\n")
	items := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		items = append(items, p)
	}
	return items
}

func formatCommandString(args []string) string {
	return strings.Join(args, " ")
}

func parseListCheck(raw string) ([]string, error) {
	if raw == "" {
		return []string{}, nil
	}
	var list []string
	for _, line := range strings.Split(raw, "\n") {
		item := strings.TrimSpace(line)
		if item == "" {
			continue
		}
		list = append(list, item)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no services returned")
	}
	return list, nil
}
