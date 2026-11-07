module github.com/hurf/work-anywhere/mobile/core

go 1.26.0

toolchain go1.27.3

require github.com/vual/work-anywhere/relay v0.0.0

require (
	github.com/coder/websocket v1.8.15 // indirect
	github.com/flynn/noise v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/mobile v0.0.0-20260908204917-8b95e45f8d3e // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/vual/work-anywhere/relay => ../../relay

tool golang.org/x/mobile/cmd/gobind
