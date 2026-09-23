//go:build goweb

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/mermaid"
)

// TestGowebGroupCreateSequence checks that POST /{lang}/groups reads handler
// → GroupService → GroupMembershipRepository → INSERT groups.
func TestGowebGroupCreateSequence(t *testing.T) {
	dir, _ := fixture.Goweb(t)
	r, err := analysis.Analyze(t.Context(), dir, analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	rec := httptest.NewRecorder()
	New(r, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/diagrams/flow?route="+url.QueryEscape("POST /{lang}/groups"), nil))
	var d mermaid.Diagram
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	order := []string{"(*groupHandler).create", "GroupCreator.Create → (*GroupService).Create",
		"GroupMembershipRepository.CreateGroupWithAdmin → (*GroupMembershipRepository).CreateGroupWithAdmin",
		"postgres->>Database: INSERT groups"}
	at := 0
	for _, want := range order {
		i := strings.Index(d.Mermaid[at:], want)
		if i < 0 {
			t.Fatalf("%q missing (in order) from:\n%s", want, d.Mermaid)
		}
		at += i
	}
	if len(d.IDs) == 0 {
		t.Error("no numbered messages")
	}
}

// TestGowebGroupsTable checks that the groups table shows its
// group_members relation and the routes that write to it.
func TestGowebGroupsTable(t *testing.T) {
	dir, _ := fixture.Goweb(t)
	r, err := analysis.Analyze(t.Context(), dir, analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	rec := httptest.NewRecorder()
	New(r, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/table?name=groups", nil))
	var d TableDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	var refBy []string
	for _, nb := range d.FKsIn {
		refBy = append(refBy, nb.Node.Name)
	}
	if !strings.Contains(strings.Join(refBy, ","), "group_members") {
		t.Errorf("groups referenced by %v, want group_members", refBy)
	}
	writes := map[string]bool{}
	for _, tr := range d.Routes {
		if tr.Op != "select" {
			writes[tr.Route+" "+tr.Op] = true
		}
	}
	for _, want := range []string{"POST /{lang}/groups insert", "POST /{lang}/groups/{id}/rename update"} {
		if !writes[want] {
			t.Errorf("groups writers %v lack %q", writes, want)
		}
	}
}
