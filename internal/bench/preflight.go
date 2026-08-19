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
//
// A flag that fails alone gets one more chance against the whole set, because
// some flags are only legal in company. -XX:NodeLimitFudgeFactor must be 2-40%
// of -XX:MaxNodeLimit, so raising MaxNodeLimit alone is rejected against the
// default fudge factor while the pair together is accepted. Without the retry,
// preflight drops half of an interdependent pair and the sweep then measures a
// configuration nobody asked for, worse than either keeping or dropping both,
// because the row still looks plausible.
func Preflight(image string, flags []string, probe Prober) (ok, dropped []string) {
	// Ask about the whole set first. When it is accepted there is nothing to
	// drop, and this costs one JVM start instead of one per flag.
	if len(flags) > 0 && probe(image, flags) == nil {
		return flags, nil
	}
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
// rejects it for being locked rather than for being unknown, which would make
// preflight drop perfectly good flags.
var Unlockers = []string{"-XX:+UnlockExperimentalVMOptions", "-XX:+UnlockDiagnosticVMOptions"}

// ProbeArgs builds the argument list for probing candidate flags. It takes a
// set rather than one flag so the whole-set probe above is expressible; the
// unlockers are prepended once and never duplicated from the candidates.
func ProbeArgs(flags []string) []string {
	args := append([]string{}, Unlockers...)
	for _, f := range flags {
		if f == Unlockers[0] || f == Unlockers[1] {
			continue
		}
		args = append(args, f)
	}
	return append(args, "-version")
}
