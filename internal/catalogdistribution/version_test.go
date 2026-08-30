package catalogdistribution

import "testing"

func TestIsNewerVersionRejectsReplayAndAcceptsRollForward(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		candidate string
		want      bool
	}{
		{name: "new release", current: "2.4.0", candidate: "2.4.1", want: true},
		{name: "same release", current: "2.4.1", candidate: "2.4.1", want: false},
		{name: "replayed release", current: "2.4.1", candidate: "2.4.0", want: false},
		{name: "embedded migration", current: "unknown", candidate: "2.4.0", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := IsNewerVersion(test.current, test.candidate)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("IsNewerVersion(%q, %q) = %v", test.current, test.candidate, got)
			}
		})
	}
}

func TestIsNewerVersionRejectsInvalidCandidate(t *testing.T) {
	if _, err := IsNewerVersion("2.4.0", "latest"); err == nil {
		t.Fatal("IsNewerVersion() error = nil")
	}
}
