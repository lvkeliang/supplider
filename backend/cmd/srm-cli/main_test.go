package main

// Unit tests for the blank-id guard shared by the id-taking CLI commands.

import (
	"flag"
	"testing"
)

func parseFlags(t *testing.T, args ...string) *flag.FlagSet {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return fs
}

func TestRejectBlankIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		n    int
		want bool // true = error expected
	}{
		{"single valid id", []string{"sup_1"}, 1, false},
		{"blank single id", []string{""}, 1, true},
		{"whitespace id", []string{"   "}, 1, true},
		{"two valid ids", []string{"sup_1", "sup_2"}, 2, false},
		{"blank second id", []string{"sup_1", ""}, 2, true},
		{"blank first id", []string{"", "sup_2"}, 2, true},
		{"flag before id", []string{"--json", "sup_1"}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := parseFlags(t, tc.args...)
			err := rejectBlankIDs(fs, tc.n)
			if (err != nil) != tc.want {
				t.Errorf("rejectBlankIDs(%v, %d) err=%v, want error=%v", tc.args, tc.n, err, tc.want)
			}
		})
	}
}

func TestJSONCount(t *testing.T) {
	if got := jsonCount([]byte(`{"format":"supplider-export","count":42,"suppliers":[]}`)); got != "42" {
		t.Errorf("valid bundle count = %q, want 42", got)
	}
	if got := jsonCount([]byte(`not json`)); got != "?" {
		t.Errorf("invalid body count = %q, want ?", got)
	}
	if got := jsonCount(nil); got != "?" {
		t.Errorf("nil body count = %q, want ?", got)
	}
}
