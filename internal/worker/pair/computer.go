package pair

import (
	"os"
	"strconv"
	"strings"
)

type computerInfo struct {
	hostname  string
	osName    string
	osVersion string
	request   map[string]string
}

var (
	readHostname  = os.Hostname
	readOSRelease = func() ([]byte, error) { return os.ReadFile("/etc/os-release") }
)

func computerDetails() computerInfo {
	hostname, err := readHostname()
	if err != nil {
		hostname = ""
	}
	hostname = truncate(hostname, 64)

	osName, osVersion := "", ""
	if data, err := readOSRelease(); err == nil {
		values := parseOSRelease(data)
		osName = values["NAME"]
		osVersion = values["VERSION_ID"]
	}
	osName = truncate(osName, 64)
	osVersion = truncate(osVersion, 32)

	request := make(map[string]string, 3)
	if hostname != "" {
		request["hostname"] = hostname
	}
	if osName != "" {
		request["os_name"] = osName
	}
	if osVersion != "" {
		request["os_version"] = osVersion
	}
	return computerInfo{hostname: hostname, osName: osName, osVersion: osVersion, request: request}
}

func parseOSRelease(data []byte) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			} else {
				value = value[1 : len(value)-1]
			}
		} else if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}
		values[name] = value
	}
	return values
}

func truncate(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-1]) + "…"
}

func displayOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func displayOS(name, version string) string {
	if name == "" && version == "" {
		return "unknown"
	}
	return displayOrUnknown(name) + " " + displayOrUnknown(version)
}
