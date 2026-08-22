package bench

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Hardware is the machine a measurement was taken on.
//
// It is recorded per result rather than once per report, because a sharded
// sweep runs on several boxes at once and they are not guaranteed to be the
// same. Two of them were not: both new boxes came up as VM.Standard.A2.Flex
// instead of A1, and A2 is AmpereOne with two vCPUs per OCPU against A1's one.
// Two OCPUs therefore means four threads rather than two, and a table that
// mixes the two silently compares a machine we ship against one we do not.
//
// It is also the property some mods actually depend on. Async parallelises
// entity ticking and works well on a twelve-thread desktop; on two cores its
// workers contend with the server thread that is waiting for them, and the tick
// deadlocks the moment a player joins. Recording cores turns "this mod needs
// cores" from folklore into a column.
type Hardware struct {
	Model          string `json:"model,omitempty"`
	Arch           string `json:"arch,omitempty"`
	CPUs           int    `json:"cpus"`
	ThreadsPerCore int    `json:"threads_per_core,omitempty"`
	MemoryMB       int    `json:"memory_mb,omitempty"`
	Kernel         string `json:"kernel,omitempty"`
}

// String is the one-line form used in report headers.
func (h Hardware) String() string {
	if h.CPUs == 0 && h.Model == "" {
		return "unknown"
	}
	parts := []string{}
	if h.Model != "" {
		parts = append(parts, h.Model)
	}
	cpu := fmt.Sprintf("%d vCPU", h.CPUs)
	if h.ThreadsPerCore > 1 {
		cpu += fmt.Sprintf(" (%d threads/core)", h.ThreadsPerCore)
	}
	parts = append(parts, cpu)
	if h.MemoryMB > 0 {
		parts = append(parts, fmt.Sprintf("%.0f GB", float64(h.MemoryMB)/1024))
	}
	if h.Arch != "" {
		parts = append(parts, h.Arch)
	}
	return strings.Join(parts, ", ")
}

// Comparable reports whether two measurements can honestly sit in one table.
//
// Model and thread count are what decide it. Kernel version and a few hundred
// MB of RAM do not change what a collector does; silicon and core count do.
func (h Hardware) Comparable(o Hardware) bool {
	return h.Model == o.Model && h.CPUs == o.CPUs && h.ThreadsPerCore == o.ThreadsPerCore
}

// HardwareSpread returns the distinct machines a set of results was measured on.
//
// More than one means the table is comparing across hardware, which is worth
// saying out loud rather than leaving for someone to notice.
func HardwareSpread(rs []Result) []Hardware {
	var out []Hardware
	for _, r := range rs {
		if r.Hardware.CPUs == 0 && r.Hardware.Model == "" {
			continue
		}
		seen := false
		for _, h := range out {
			if h.Comparable(r.Hardware) {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, r.Hardware)
		}
	}
	return out
}

var (
	lscpuField = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`(?mi)^` + name + `:\s*(.+?)\s*$`)
	}
	memTotal = regexp.MustCompile(`(?m)^MemTotal:\s+(\d+)\s*kB`)
)

// ParseLscpu reads the fields worth keeping out of lscpu output.
//
// Parsing text rather than shelling out to something structured because lscpu
// is present on every box here and its labels have been stable for years,
// whereas the alternatives are either another dependency or /proc/cpuinfo,
// which differs between x86 and aarch64 in exactly the fields wanted.
func ParseLscpu(s string) Hardware {
	var h Hardware
	get := func(name string) string {
		if m := lscpuField(name).FindStringSubmatch(s); m != nil {
			return m[1]
		}
		return ""
	}
	h.Arch = get("Architecture")
	h.Model = get("Model name")
	if h.Model == "" {
		// aarch64 lscpu sometimes only carries the implementer and part.
		h.Model = get("Vendor ID")
	}
	h.CPUs, _ = strconv.Atoi(get(`CPU\(s\)`))
	h.ThreadsPerCore, _ = strconv.Atoi(get(`Thread\(s\) per core`))
	return h
}

// ParseMemTotal reads total memory in MB from /proc/meminfo.
func ParseMemTotal(s string) int {
	m := memTotal.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	kb, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return kb / 1024
}
