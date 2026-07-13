package app

import (
	"fmt"
	"log/slog"
	"runtime"

	"github.com/grafana/pyroscope-go"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

// pyroscopeProfileTypes returns the profile types pushed to Pyroscope: the
// standard CPU + alloc/inuse memory set plus goroutines, adding the mutex and
// block profiles only when their runtime fractions are enabled (collecting them
// otherwise would just push empty profiles).
func pyroscopeProfileTypes(p config.ProfilingConfig) []pyroscope.ProfileType {
	types := []pyroscope.ProfileType{
		pyroscope.ProfileCPU,
		pyroscope.ProfileAllocObjects,
		pyroscope.ProfileAllocSpace,
		pyroscope.ProfileInuseObjects,
		pyroscope.ProfileInuseSpace,
		pyroscope.ProfileGoroutines,
	}
	if p.MutexProfileFraction > 0 {
		types = append(types, pyroscope.ProfileMutexCount, pyroscope.ProfileMutexDuration)
	}
	if p.BlockProfileRate > 0 {
		types = append(types, pyroscope.ProfileBlockCount, pyroscope.ProfileBlockDuration)
	}
	return types
}

// pyroscopeConfig maps the profiling config into a pyroscope.Config. It is pure
// (no side effects, no Logger) so the mapping is unit-testable; the live logger
// is attached by startProfiling. service_version is always tagged and cannot be
// overridden by a user tag.
func pyroscopeConfig(cfg *config.Config, version string) pyroscope.Config {
	p := cfg.Profiling.Pyroscope
	tags := map[string]string{"service_version": version}
	for k, v := range p.Tags {
		if k != "service_version" {
			tags[k] = v
		}
	}
	pc := pyroscope.Config{
		ApplicationName:   serviceName,
		ServerAddress:     p.ServerAddress,
		BasicAuthUser:     p.BasicAuthUser,
		BasicAuthPassword: p.BasicAuthPassword.Reveal(),
		TenantID:          p.TenantID,
		Tags:              tags,
		ProfileTypes:      pyroscopeProfileTypes(cfg.Profiling),
	}
	if d := p.UploadRate.D(); d > 0 {
		pc.UploadRate = d
	}
	return pc
}

// startProfiling applies the runtime mutex/block profiling rates (needed by both
// the Pyroscope push and the /debug/pprof pull paths) and, when Pyroscope push
// is enabled, starts the continuous profiler. It returns the profiler (nil when
// push is disabled) so the caller can Stop it on shutdown.
func startProfiling(cfg *config.Config, version string, logger *slog.Logger) (*pyroscope.Profiler, error) {
	prof := cfg.Profiling
	if prof.MutexProfileFraction > 0 {
		runtime.SetMutexProfileFraction(prof.MutexProfileFraction)
	}
	if prof.BlockProfileRate > 0 {
		runtime.SetBlockProfileRate(prof.BlockProfileRate)
	}
	if !prof.Pyroscope.Enabled {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	pc := pyroscopeConfig(cfg, version)
	pc.Logger = pyroscopeLogger{l: logger}
	return pyroscope.Start(pc)
}

// pyroscopeLogger adapts *slog.Logger to the pyroscope.Logger interface.
type pyroscopeLogger struct{ l *slog.Logger }

func (p pyroscopeLogger) Infof(format string, args ...any)  { p.l.Info(fmt.Sprintf(format, args...)) }
func (p pyroscopeLogger) Debugf(format string, args ...any) { p.l.Debug(fmt.Sprintf(format, args...)) }
func (p pyroscopeLogger) Errorf(format string, args ...any) { p.l.Error(fmt.Sprintf(format, args...)) }
