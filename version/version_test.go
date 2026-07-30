package version

import "testing"

func TestValidateNameRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../outside", "nested/name", `nested\name`} {
		t.Run(name, func(t *testing.T) {
			if err := validateName(name); err == nil {
				t.Fatalf("validateName(%q) unexpectedly succeeded", name)
			}
		})
	}

	if err := validateName("pre-pull-20260730-120000"); err != nil {
		t.Fatalf("valid snapshot name rejected: %v", err)
	}
}
