//go:build unix

package app

import "syscall"

// tvToSeconds converts a syscall.Timeval to fractional seconds.
// Using int64() casts to handle both Linux (int64 Usec) and Darwin (int32 Usec).
func tvToSeconds(tv syscall.Timeval) float64 {
	return float64(tv.Sec) + float64(int64(tv.Usec))/1e6
}

// readProcessCPU returns the cumulative user and system CPU time for the current
// process, in seconds, by calling getrusage(RUSAGE_SELF). Returns ok=false on error.
func readProcessCPU() (user, system float64, ok bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0, false
	}
	return tvToSeconds(ru.Utime), tvToSeconds(ru.Stime), true
}
