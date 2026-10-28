package domain

import (
	"regexp"
	"strings"
)

var (
	rifValid = regexp.MustCompile(`^[JVEGPC]-\d{8}-\d$`)
	rifParts = regexp.MustCompile(`^([JVEGPC])(\d{8})(\d)$`)
	rifNoise = regexp.MustCompile(`[^JVEGPC0-9]`)
)

// NormalizeRIF formats a Venezuelan RIF as J-12345678-9 when it can.
func NormalizeRIF(v string) string {
	clean := rifNoise.ReplaceAllString(strings.ToUpper(v), "")
	if m := rifParts.FindStringSubmatch(clean); m != nil {
		return m[1] + "-" + m[2] + "-" + m[3]
	}
	return strings.ToUpper(strings.TrimSpace(v))
}

func ValidRIF(v string) bool { return rifValid.MatchString(NormalizeRIF(v)) }
