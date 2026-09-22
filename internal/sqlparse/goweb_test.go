//go:build goweb

package sqlparse

import (
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
)

func TestGowebSchemaMatchesExpectations(t *testing.T) {
	dir, exp := fixture.Goweb(t)
	checkSchema(t, dir, exp)
}
