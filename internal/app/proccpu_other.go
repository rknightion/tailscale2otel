//go:build !unix

package app

// readProcessCPU is a no-op stub on non-unix platforms. ok=false signals the
// caller to skip emitting process.cpu.time.
func readProcessCPU() (user, system float64, ok bool) {
	return 0, 0, false
}
