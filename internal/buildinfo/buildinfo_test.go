package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestVersionDefault(t *testing.T) {
	if Version() == "" {
		t.Fatal("Version() must never be empty; an unstamped build reports \"dev\"")
	}
}

func TestCommitFallsBackToVCSStamp(t *testing.T) {
	// With commit set by ldflags, that value wins verbatim.
	t.Run("ldflags wins", func(t *testing.T) {
		old := commit
		t.Cleanup(func() { commit = old })
		commit = "0123456789abcdef"
		if got := Commit(); got != "0123456789abcdef" {
			t.Fatalf("Commit() = %q, want the ldflags value", got)
		}
	})
	// Without it, Commit consults the embedded build info. Under `go test` that
	// may or may not carry a vcs.revision, so the contract is only that it does
	// not panic and returns something printable.
	t.Run("no ldflags", func(t *testing.T) {
		old := commit
		t.Cleanup(func() { commit = old })
		commit = ""
		_ = Commit()
	})
}

func TestString(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		commit      string
		wantContain []string
		wantAbsent  string
	}{
		{
			name:        "stamped release",
			version:     "v1.2.3",
			commit:      "abcdef0123456789abcdef",
			wantContain: []string{"wly v1.2.3", "(abcdef012345)", runtime.GOOS},
		},
		{
			name:        "short commit is not truncated",
			version:     "v1.2.3",
			commit:      "abc123",
			wantContain: []string{"(abc123)"},
		},
		{
			name:        "dev build with no commit omits the parens",
			version:     "dev",
			commit:      "",
			wantContain: []string{"wly dev", runtime.Version()},
			wantAbsent:  "(",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldV, oldC := version, commit
			t.Cleanup(func() { version, commit = oldV, oldC })
			version, commit = tt.version, tt.commit

			got := String("wly")
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("String() = %q, want it to contain %q", got, want)
				}
			}
			// Only meaningful when the build info carries no revision either;
			// otherwise the fallback legitimately supplies one.
			if tt.wantAbsent != "" && Commit() == "" && strings.Contains(got, tt.wantAbsent) {
				t.Errorf("String() = %q, want no %q", got, tt.wantAbsent)
			}
		})
	}
}
