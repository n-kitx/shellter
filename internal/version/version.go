// Package version is Shellter's version number, the one place it is kept.
//
// A release sets it when building (scripts/release.sh, through bundle.sh):
//
//	go build -ldflags "-X github.com/n-kitx/shellter/internal/version.Version=0.1.0"
//
// Anything else - go run, go test, a plain bundle.sh - is "dev".
package version

// Version is the release, without a leading "v": "0.1.0".
var Version = "dev"
