package graph

// NodeKind classifies a node in the code graph.
type NodeKind string

// Node kinds. See docs/PLAN.md §3.
const (
	KindRoute         NodeKind = "route"          // HTTP method + pattern registered on a router
	KindPage          NodeKind = "page"           // user-facing page (a GET route rendering a template)
	KindHTMXCall      NodeKind = "htmx_call"      // request a page makes (hx-get/post, form action)
	KindStaticAsset   NodeKind = "static_asset"   // CSS, JS, image served to the browser
	KindTemplate      NodeKind = "template"       // html/template file or named template
	KindHandler       NodeKind = "handler"        // function registered as an HTTP handler
	KindFunc          NodeKind = "func"           // package-level function or closure
	KindMethod        NodeKind = "method"         // method on a concrete type
	KindInterfaceCall NodeKind = "interface_call" // interface method, resolved via dispatches_to
	KindSinkSQL       NodeKind = "sink.sql"       // database query or exec call site
	KindSinkFile      NodeKind = "sink.file"      // file or embed.FS read/write
	KindSinkHTTP      NodeKind = "sink.http"      // outbound HTTP request
	KindSinkSMTP      NodeKind = "sink.smtp"      // outbound mail
	KindSinkExec      NodeKind = "sink.exec"      // external process
	KindSinkEnv       NodeKind = "sink.env"       // environment/config read
	KindType          NodeKind = "type"           // named Go type: struct, interface, ...
	KindSQLTable      NodeKind = "sql_table"      // database table from migrations
	KindSQLColumn     NodeKind = "sql_column"     // database column
)

var nodeKinds = map[NodeKind]bool{
	KindRoute: true, KindPage: true, KindHTMXCall: true, KindStaticAsset: true, KindTemplate: true,
	KindHandler: true, KindFunc: true, KindMethod: true, KindInterfaceCall: true,
	KindSinkSQL: true, KindSinkFile: true, KindSinkHTTP: true, KindSinkSMTP: true, KindSinkExec: true, KindSinkEnv: true,
	KindType: true, KindSQLTable: true, KindSQLColumn: true,
}

// Valid reports whether k is a known node kind.
func (k NodeKind) Valid() bool { return nodeKinds[k] }

// EdgeKind classifies a directed edge between two nodes.
type EdgeKind string

// Edge kinds. See docs/PLAN.md §3.
const (
	EdgeNavigatesTo  EdgeKind = "navigates_to"  // page → page via link, form or redirect
	EdgeRequests     EdgeKind = "requests"      // page → htmx_call
	EdgeLoads        EdgeKind = "loads"         // page → static_asset
	EdgeHandledBy    EdgeKind = "handled_by"    // route/htmx_call → handler
	EdgeCalls        EdgeKind = "calls"         // func → func, at a call site
	EdgeImplements   EdgeKind = "implements"    // concrete type → interface type
	EdgeDispatchesTo EdgeKind = "dispatches_to" // interface_call → concrete method
	EdgeRenders      EdgeKind = "renders"       // handler → template
	EdgeUsesType     EdgeKind = "uses_type"     // func → type
	EdgeQueries      EdgeKind = "queries"       // sink.sql → sql_table
	EdgeHasColumn    EdgeKind = "has_column"    // sql_table → sql_column
	EdgeFK           EdgeKind = "fk"            // sql_table → referenced sql_table
	EdgeGuardedBy    EdgeKind = "guarded_by"    // route → auth guard func
)

var edgeKinds = map[EdgeKind]bool{
	EdgeNavigatesTo: true, EdgeRequests: true, EdgeLoads: true, EdgeHandledBy: true, EdgeCalls: true,
	EdgeImplements: true, EdgeDispatchesTo: true, EdgeRenders: true, EdgeUsesType: true,
	EdgeQueries: true, EdgeHasColumn: true, EdgeFK: true, EdgeGuardedBy: true,
}

// Valid reports whether k is a known edge kind.
func (k EdgeKind) Valid() bool { return edgeKinds[k] }
