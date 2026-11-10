// Mirror of desktop/internal/update/manifest (the protocol owner). Keep the
// two in sync; both test suites run desktop/docs/self-update/vectors.json.
package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed SemVer 2.0 version.
type Version struct {
	Major, Minor, Patch uint64
	Prerelease          string
	Build               string
}

// ParseVersion parses a SemVer 2.0 string without a leading "v".
func ParseVersion(raw string) (Version, error) {
	var version Version
	rest := raw
	if index := strings.IndexByte(rest, '+'); index >= 0 {
		version.Build = rest[index+1:]
		rest = rest[:index]
		if !validIdentifiers(version.Build, false) {
			return Version{}, fmt.Errorf("invalid build metadata in %q", raw)
		}
	}
	if index := strings.IndexByte(rest, '-'); index >= 0 {
		version.Prerelease = rest[index+1:]
		rest = rest[:index]
		if !validIdentifiers(version.Prerelease, true) {
			return Version{}, fmt.Errorf("invalid prerelease in %q", raw)
		}
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version %q must have three numeric parts", raw)
	}
	numbers := make([]uint64, 3)
	for i, part := range parts {
		if !numeric(part) || (len(part) > 1 && part[0] == '0') {
			return Version{}, fmt.Errorf("invalid numeric part %q in %q", part, raw)
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("invalid numeric part %q in %q", part, raw)
		}
		numbers[i] = value
	}
	version.Major, version.Minor, version.Patch = numbers[0], numbers[1], numbers[2]
	return version, nil
}

func numeric(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validIdentifiers(value string, rejectLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		for _, r := range identifier {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
				return false
			}
		}
		if rejectLeadingZero && numeric(identifier) && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

// Compare returns -1, 0 or 1 following SemVer 2.0 precedence. Build metadata
// is ignored.
func Compare(a, b Version) int {
	for _, pair := range [][2]uint64{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.Prerelease == b.Prerelease:
		return 0
	case a.Prerelease == "":
		return 1
	case b.Prerelease == "":
		return -1
	}
	left, right := strings.Split(a.Prerelease, "."), strings.Split(b.Prerelease, ".")
	for i := 0; i < len(left) && i < len(right); i++ {
		if result := compareIdentifier(left[i], right[i]); result != 0 {
			return result
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	}
	return 0
}

func compareIdentifier(a, b string) int {
	aNumeric, bNumeric := numeric(a), numeric(b)
	switch {
	case aNumeric && bNumeric:
		if len(a) != len(b) {
			if len(a) < len(b) {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	case aNumeric:
		return -1
	case bNumeric:
		return 1
	}
	return strings.Compare(a, b)
}

// IsNewer reports whether candidate is strictly newer than current. An
// unparsable current version is reported as an error so callers can disable
// automatic updates for development builds.
func IsNewer(candidate, current string) (bool, error) {
	next, err := ParseVersion(candidate)
	if err != nil {
		return false, err
	}
	installed, err := ParseVersion(strings.TrimPrefix(current, "v"))
	if err != nil {
		return false, err
	}
	return Compare(next, installed) > 0, nil
}
