package bench

import (
	"regexp"
	"strconv"
)

// itzg tags look like java21, java25, java25-graalvm, java25-alpine.
var javaTag = regexp.MustCompile(`java(\d+)`)

// javaMajor extracts the JDK major version from a container tag, or 0 when the
// tag does not say. Zero means "assume nothing", which is the safe direction:
// compat flags get skipped rather than applied to a JDK that would reject them.
func javaMajor(image string) int {
	m := javaTag.FindStringSubmatch(image)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}
