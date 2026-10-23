module github.com/hat47x/eki-fabric/spikes/claim-wire-roundtrip

go 1.27.0

require (
	github.com/bufbuild/protocompile v0.14.1
	github.com/hat47x/eki-fabric v0.0.0
	google.golang.org/protobuf v1.36.12
)

require golang.org/x/sync v0.8.0 // indirect

replace github.com/hat47x/eki-fabric => ../..
