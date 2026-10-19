package plugin

import (
	"os"
	"runtime/debug"
)

// DefaultMemoryLimit is the soft Go memory limit (runtime/debug.SetMemoryLimit)
// the plugin process runs with when the GOMEMLIMIT environment variable does
// not set one. Parsing a hostile page creates garbage several times the size of
// its live parse tree; with this limit the garbage collector reclaims it
// before the heap grows past the sizing in
// ai-state/streams/v1-stabilisation/memory-sizing.md. The limit is soft: the
// process is never stopped for exceeding it, the collector only runs more
// often.
const DefaultMemoryLimit = 256 << 20

// ApplyDefaultMemoryLimit sets DefaultMemoryLimit as the soft memory limit,
// unless GOMEMLIMIT is set in the environment (then the Go runtime has already
// applied that value). It returns the limit now in effect.
func ApplyDefaultMemoryLimit() int64 {
	if _, set := os.LookupEnv("GOMEMLIMIT"); set {
		return debug.SetMemoryLimit(-1)
	}
	debug.SetMemoryLimit(DefaultMemoryLimit)
	return DefaultMemoryLimit
}
