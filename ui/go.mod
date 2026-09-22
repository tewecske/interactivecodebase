// This file only fences ui/ off from the Go module at the repository root,
// so `go build ./...` never walks into node_modules. There is no Go code here;
// the built UI is embedded from internal/webui/dist.
module github.com/tewecske/interactivecodebase/ui

go 1.26.0
