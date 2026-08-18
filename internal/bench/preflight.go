package bench

// Prober reports whether a JVM accepts a set of flags. Injected so the decision
// logic can be tested without a container.
type Prober func(image string, flags []string) error

// Preflight returns the flags a JVM accepts, and those it rejected.
//
// This is the whole answer to "which flags did this JDK remove". The base set
// everyone copies predates JDK 25 and targets x86; rather than research each
// flag against each release, ask the JVM, which already knows and stays right
// across future bumps. A flag removed by a later JDK then degrades the profile
// instead of failing the boot.
//
// Flags are probed one at a time on purpose: probing the set only reveals that
// something in it is bad, and the point is to keep the rest.
func Preflight(image string, flags []string, probe Prober) (ok, dropped []string) {
	for _, f := range flags {
		if probe(image, []string{f}) != nil {
			dropped = append(dropped, f)
			continue
		}
		ok = append(ok, f)
	}
	return ok, dropped
}

// Unlockers must precede any experimental or diagnostic flag, or the JVM
// rejects it for being locked rather than for being unknown — which would make
// preflight drop perfectly good flags.
var Unlockers = []string{"-XX:+UnlockExperimentalVMOptions", "-XX:+UnlockDiagnosticVMOptions"}

// ProbeArgs builds the argument list for probing one candidate flag.
func ProbeArgs(flag string) []string {
	args := append([]string{}, Unlockers...)
	if flag == Unlockers[0] || flag == Unlockers[1] {
		return append(args, "-version")
	}
	return append(append(args, flag), "-version")
}
