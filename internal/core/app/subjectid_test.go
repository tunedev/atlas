package app_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

// TestCheckSubjectIDAllowsOnlyAFolderNameEveryOSAccepts pins that a subject
// id is one path segment every OS can create as a directory.
func TestCheckSubjectIDAllowsOnlyAFolderNameEveryOSAccepts(t *testing.T) {
	for _, id := range []string{"order-7", "board~bakery~101", "agent-hn-3", "rye_loaf.2"} {
		if err := app.CheckSubjectID(id); err != nil {
			t.Errorf("%q: %v; want accepted", id, err)
		}
	}
	for _, id := range []string{
		"board/bakery", "..", "a..b", `board\bakery`, "board:bakery:101",
		"rye*", "rye?", `rye"`, "rye<", "rye>", "rye|", "rye\x00", "rye\tloaf",
	} {
		if err := app.CheckSubjectID(id); err == nil {
			t.Errorf("%q: accepted; want refused", id)
		}
	}
}
