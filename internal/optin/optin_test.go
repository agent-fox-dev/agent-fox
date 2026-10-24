package optin

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// recorder is a test that remembers whether it was skipped, how many times, and why.
type recorder struct {
	skips  int
	reason string
}

func (r *recorder) Helper() {}

func (r *recorder) Skipf(format string, args ...any) {
	r.skips++
	r.reason = fmt.Sprintf(format, args...)
}

// hostWith replaces what the gates ask of the host for the rest of the test: the
// answers of the probes, and the absence of the variables.
func hostWith(t *testing.T, h host) {
	t.Helper()
	old := probe
	probe = h
	t.Cleanup(func() { probe = old })
	for _, name := range []string{"AF_TEST_LANDLOCK", "AF_TEST_PODMAN"} {
		t.Setenv(name, "")
	}
}

// capable is a host that has everything the opt-in tests ask of it.
func capable() host {
	return host{
		landlock: func() error { return nil },
		podman:   func() error { return nil },
		image:    func(string) error { return nil },
	}
}

// broken is a host that has nothing, each probe saying so in words of its own, so that a
// gate that goes on after it has skipped is seen to: it would skip again, with another reason.
func broken() host {
	return host{
		landlock: func() error { return errors.New("landlock-down") },
		podman:   func() error { return errors.New("engine-down") },
		image:    func(string) error { return errors.New("image-gone") },
	}
}

// TestAnOptInTestWithAMissingVariableOrPrerequisiteIsSkippedWithANamedReason_TS_10_64
// verifies TS-10-64: with the variable unset, or set while the prerequisite is
// absent, each gate skips the test once (never fails it) and the reason names what is
// missing and nothing that is not; with both present the test runs.
// Verifies: 10-REQ-10.4
func TestAnOptInTestWithAMissingVariableOrPrerequisiteIsSkippedWithANamedReason_TS_10_64(t *testing.T) {
	for _, tc := range []struct {
		name string
		gate func(Skipper)
		// env are the variables set; works makes the probes that work on this host work;
		// want is what the reason names, and notWant what it does not.
		env     map[string]string
		works   func(*host)
		want    []string
		notWant []string
	}{
		{"landlock, variable unset", func(s Skipper) { RequireLandlock(s) }, nil, nil, []string{"AF_TEST_LANDLOCK"}, []string{"landlock-down"}},
		{"landlock, variable not 1", func(s Skipper) { RequireLandlock(s) }, map[string]string{"AF_TEST_LANDLOCK": "yes"}, nil, []string{"AF_TEST_LANDLOCK"}, []string{"landlock-down"}},
		{"landlock, no Landlock here", func(s Skipper) { RequireLandlock(s) }, map[string]string{"AF_TEST_LANDLOCK": "1"}, nil, []string{"AF_TEST_LANDLOCK=1", "landlock-down"}, nil},
		{"podman, variable unset", func(s Skipper) { RequirePodman(s, "img:1") }, nil, nil, []string{"AF_TEST_PODMAN"}, []string{"engine-down", "image-gone"}},
		{"podman, no engine", func(s Skipper) { RequirePodman(s, "img:1") }, map[string]string{"AF_TEST_PODMAN": "1"}, nil,
			[]string{"AF_TEST_PODMAN=1", "podman info", "engine-down"}, []string{"image-gone"}},
		{"podman, no image", func(s Skipper) { RequirePodman(s, "img:1") }, map[string]string{"AF_TEST_PODMAN": "1"},
			func(h *host) { h.podman = capable().podman }, []string{"img:1", "AF_SANDBOX_IMAGE", "image-gone"}, []string{"engine-down"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := broken()
			if tc.works != nil {
				tc.works(&h)
			}
			hostWith(t, h)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			var r recorder

			tc.gate(&r)

			if r.skips != 1 {
				t.Fatalf("the gate skipped the test %d times, want once (last reason %q)", r.skips, r.reason)
			}
			for _, want := range tc.want {
				if !strings.Contains(r.reason, want) {
					t.Errorf("reason = %q, want it to name %q", r.reason, want)
				}
			}
			for _, not := range tc.notWant {
				if strings.Contains(r.reason, not) {
					t.Errorf("reason = %q, want it not to name %q", r.reason, not)
				}
			}
		})
	}

	// With the variable set and the prerequisite there, the test is not skipped.
	hostWith(t, capable())
	t.Setenv("AF_TEST_LANDLOCK", "1")
	t.Setenv("AF_TEST_PODMAN", "1")
	var r recorder
	RequireLandlock(&r)
	RequirePodman(&r, "img:1")
	if r.skips != 0 {
		t.Errorf("the gates skipped a test that has everything: %s", r.reason)
	}
}

// TestTheGatesOfTheRealHostSkipOnAHostWithNothingSet verifies the gates as the tests
// call them: with no variable set they skip a real *testing.T, and that is a skip and
// not a failure.
func TestTheGatesOfTheRealHostSkipOnAHostWithNothingSet(t *testing.T) {
	for _, name := range []string{"AF_TEST_LANDLOCK", "AF_TEST_PODMAN"} {
		t.Setenv(name, "")
	}
	for name, gate := range map[string]func(*testing.T){
		"landlock": func(t *testing.T) { RequireLandlock(t) },
		"podman":   func(t *testing.T) { RequirePodman(t, "img:1") },
	} {
		t.Run(name, func(t *testing.T) {
			gate(t)
			t.Error("the gate did not skip")
		})
	}
}
