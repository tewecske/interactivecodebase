// Package fixture gives tests access to the analysis fixtures: the webapp
// module under testdata/fixtures and the pinned goweb checkout, together with
// the hand-written expectations of what analysis must find in each.
//
// Function names use the go/ssa format: "pkg/path.Func", "(*pkg/path.T).Method",
// "pkg/path.Factory$1" for the first closure in Factory. Types are
// "pkg/path.T", with a leading "*" for pointer receivers.
package fixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Access levels of a route.
const (
	AccessPublic        = "public"        // reachable without a session
	AccessAuthenticated = "authenticated" // requires a signed-in user
	AccessAdmin         = "admin"         // requires an administrator
	AccessGuest         = "guest"         // requires a signed-in guest user
)

// Expectations is what analysis must find in one module.
type Expectations struct {
	Module string `json:"module"`
	// Commit pins the source revision the expectations were written against.
	Commit          string           `json:"commit,omitempty"`
	Routes          []Route          `json:"routes"`
	Middleware      []string         `json:"middleware,omitempty"`
	Tables          []string         `json:"tables"`
	ForeignKeys     []ForeignKey     `json:"foreignKeys"`
	Implementations []Implementation `json:"implementations"`
	Sinks           []Sink           `json:"sinks"`
	Flows           []Flow           `json:"flows"`
	Pages           []Page           `json:"pages,omitempty"`
}

// Route is one registered method + pattern.
type Route struct {
	Method string `json:"method"`
	// Pattern is the path pattern. Language-prefix loops collapse into
	// "/{lang}"; parts that cannot be resolved statically become "{?}".
	Pattern string `json:"pattern"`
	// Variants are the concrete patterns a collapsed route stands for.
	Variants []string `json:"variants,omitempty"`
	Handler  string   `json:"handler"`
	Access   string   `json:"access"`
	// OptionalAuth marks public routes that still look up the session,
	// e.g. to personalise the page or redirect signed-in users.
	OptionalAuth bool `json:"optionalAuth,omitempty"`
	// Middleware wrapping only this route, outermost first.
	Middleware []string `json:"middleware,omitempty"`
	// Conditional routes are registered only for some runtime configuration.
	Conditional bool `json:"conditional,omitempty"`
	// Static routes serve files.
	Static bool `json:"static,omitempty"`
}

// Key identifies a route as "METHOD pattern".
func (r Route) Key() string { return r.Method + " " + r.Pattern }

// ForeignKey is one column reference between tables.
type ForeignKey struct {
	Table      string `json:"table"`
	Column     string `json:"column"`
	References string `json:"references"` // "table.column"
	OnDelete   string `json:"onDelete"`   // "cascade", "set null", "" for no action
}

// Implementation states that a concrete type implements an interface.
type Implementation struct {
	Interface string `json:"interface"`
	Concrete  string `json:"concrete"`
}

// Sink is a terminal call analysis must detect.
type Sink struct {
	Kind   string   `json:"kind"` // a graph.NodeKind, e.g. "sink.sql"
	In     string   `json:"in"`   // the function containing the call
	Tables []string `json:"tables,omitempty"`
	Op     string   `json:"op,omitempty"` // select, insert, update, delete
	Detail string   `json:"detail,omitempty"`
}

// Flow lists what a route's call flow must reach (a subset, not all of it).
type Flow struct {
	Route   string  `json:"route"` // Route.Key()
	Reaches []Reach `json:"reaches"`
}

// Reach is a table operation or a non-SQL sink kind.
type Reach struct {
	Table string `json:"table,omitempty"`
	Op    string `json:"op,omitempty"`
	Sink  string `json:"sink,omitempty"`
}

// Page is a rendered page and what it links to.
type Page struct {
	Route       string    `json:"route"` // Route.Key()
	Template    string    `json:"template"`
	Requests    []Request `json:"requests"`
	NavigatesTo []string  `json:"navigatesTo"` // Route.Key() values
	Assets      []string  `json:"assets"`
}

// Request is a request a page can make.
type Request struct {
	Method  string `json:"method"`
	URL     string `json:"url"`
	Trigger string `json:"trigger"` // form, hx-get, hx-post, ...
}

// Load reads expectations from a JSON file, rejecting unknown fields.
func Load(path string) (Expectations, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Expectations{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var e Expectations
	if err := dec.Decode(&e); err != nil {
		return Expectations{}, fmt.Errorf("fixture: %s: %w", path, err)
	}
	return e, nil
}
