module example.com/entries

go 1.24

require (
	github.com/nats-io/nats.go v1.0.0
	github.com/spf13/cobra v1.0.0
	google.golang.org/grpc v1.0.0
)

replace (
	github.com/nats-io/nats.go => ./stubs/nats
	github.com/spf13/cobra => ./stubs/cobra
	google.golang.org/grpc => ./stubs/grpc
)
