// Package buildinfo contains release identity injected by official builds.
package buildinfo

var Version = "dev"
var SourceCommit = "unknown"

// PAHS0WebBundle is set by the same immutable Docker build argument that
// compiles the embedded Web assets. An environment variable cannot override it.
var PAHS0WebBundle = "0"
