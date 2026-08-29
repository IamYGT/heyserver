package releaseversion

import (
	"strconv"
	"strings"
)

type State string

const (
	Current State = "current"
	Behind  State = "behind"
	Ahead   State = "ahead"
	Unknown State = "unknown"
)

// Compare reports the candidate release's position relative to the current
// release. Only stable major.minor.patch versions have a reliable ordering.
func Compare(candidateVersion, currentVersion string) State {
	candidate, candidateOK := parse(candidateVersion)
	current, currentOK := parse(currentVersion)
	if !candidateOK || !currentOK {
		return Unknown
	}
	for index := range candidate {
		if candidate[index] < current[index] {
			return Behind
		}
		if candidate[index] > current[index] {
			return Ahead
		}
	}
	return Current
}

func parse(value string) ([3]uint64, bool) {
	var version [3]uint64
	normalized := strings.TrimPrefix(strings.TrimSpace(value), "v")
	if strings.ContainsAny(normalized, "-+") {
		return version, false
	}
	parts := strings.Split(normalized, ".")
	if len(parts) != len(version) {
		return version, false
	}
	for index, part := range parts {
		if part == "" {
			return version, false
		}
		parsed, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version, false
		}
		version[index] = parsed
	}
	return version, true
}
