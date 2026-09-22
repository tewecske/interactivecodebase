//go:build goweb

package fixture

import "testing"

// TestGowebExpectationsMatchSource runs with -tags goweb against a goweb
// checkout at the pinned commit (see Goweb).
func TestGowebExpectationsMatchSource(t *testing.T) {
	dir, exp := Goweb(t)
	assertValid(t, dir, exp)
}
