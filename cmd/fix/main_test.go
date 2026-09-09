package main

import (
	"reflect"
	"testing"
)

func TestNormalizeArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "no pull flag",
			in:   []string{"-repo", "owner/repo", "bug description"},
			want: []string{"-repo", "owner/repo", "bug description"},
		},
		{
			name: "pull with equal and branch",
			in:   []string{"--pull=main", "bug description"},
			want: []string{"--pull=main", "bug description"},
		},
		{
			name: "single argument bare pull (help/no input)",
			in:   []string{"-pull"},
			want: []string{"-pull"},
		},
		{
			name: "pull with one positional input",
			in:   []string{"-pull", "bug description"},
			want: []string{"-pull", "bug description"},
		},
		{
			name: "pull with one positional and other flags",
			in:   []string{"-repo", "owner/repo", "-pull", "bug description"},
			want: []string{"-repo", "owner/repo", "-pull", "bug description"},
		},
		{
			name: "pull with branch and one positional input",
			in:   []string{"-pull", "dev", "bug description"},
			want: []string{"-pull=dev", "bug description"},
		},
		{
			name: "double dash pull with branch and one positional input",
			in:   []string{"--pull", "dev", "bug description"},
			want: []string{"--pull=dev", "bug description"},
		},
		{
			name: "pull with branch and flag before input",
			in:   []string{"-pull", "dev", "--dry-run", "bug description"},
			want: []string{"-pull=dev", "--dry-run", "bug description"},
		},
		{
			name: "flags before pull with branch and input",
			in:   []string{"--dry-run", "-pull", "dev", "bug description"},
			want: []string{"--dry-run", "-pull=dev", "bug description"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeArgs(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("normalizeArgs(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPullFlagParsing(t *testing.T) {
	cases := []struct {
		name       string
		val        string
		wantSet    bool
		wantBranch string
		wantErr    bool
	}{
		{
			name:       "empty bool string",
			val:        "",
			wantSet:    true,
			wantBranch: "",
		},
		{
			name:       "true bool string",
			val:        "true",
			wantSet:    true,
			wantBranch: "",
		},
		{
			name:       "false bool string",
			val:        "false",
			wantSet:    false,
			wantBranch: "",
		},
		{
			name:       "branch string",
			val:        "main",
			wantSet:    true,
			wantBranch: "main",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var pf pullFlag
			err := pf.Set(tc.val)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Set(%q) err = %v, wantErr %v", tc.val, err, tc.wantErr)
			}
			if pf.set != tc.wantSet {
				t.Errorf("pf.set = %v, want %v", pf.set, tc.wantSet)
			}
			if pf.branch != tc.wantBranch {
				t.Errorf("pf.branch = %q, want %q", pf.branch, tc.wantBranch)
			}
		})
	}
}
