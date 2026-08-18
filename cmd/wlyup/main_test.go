package main

import (
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantOut string
		wantErr string
	}{
		{name: "version prints the stamp", args: []string{"version"}, wantOut: "wlyup "},
		{name: "no args is not yet implemented", args: nil, wantErr: "not implemented"},
		{name: "unknown command is rejected", args: []string{"nope"}, wantErr: `unknown command "nope"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			err := run(tt.args, &out)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("run(%q) error = %v, want it to contain %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("run(%q) = %v, want no error", tt.args, err)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("run(%q) wrote %q, want it to contain %q", tt.args, out.String(), tt.wantOut)
			}
		})
	}
}
