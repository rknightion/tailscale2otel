package app

import (
	"fmt"
	"log/slog"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"

	"github.com/grafana/pyroscope-go"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// goroutineLeakAvailable reports whether the runtime exposes the goroutineleak
// profile. It is registered only when the binary is built with
// GOEXPERIMENT=goroutineleakprofile (Go 1.26+); a binary built without it simply
// omits the type instead of pushing an empty/erroring profile. Release builds and
// the container image set the experiment; a plain `go build` does not.
func goroutineLeakAvailable() bool {
	return pprof.Lookup("goroutineleak") != nil
}

// pyroscopeProfileTypes returns the profile types pushed to Pyroscope: the
// standard CPU + alloc/inuse memory set plus goroutines, the mutex and block
// profiles when their runtime fractions are enabled (on by default — see
// config.Default; collecting them with the fraction off would just push empty
// profiles), and goroutine-leak when the runtime exposes it (built with the
// experiment).
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
	if goroutineLeakAvailable() {
		types = append(types, pyroscope.ProfileGoroutineLeak)
	}
	return types
}

// reservedPyroscopeTags are the profile-identity tags a user tag may never
// displace (#374).
//
// service_version was already protected; service_instance_id is the gap #374
// closed. Without it a profile can be attributed to a build but not to a
// PROCESS, which is exactly the question worth asking in a multi-replica or
// multi-tailnet deployment — "which of the five replicas is burning the CPU".
// Both are reserved rather than merely defaulted: a user tag that silently
// overwrote them would break the join to every other signal, which carries the
// same two values as service.version / service.instance.id.
var reservedPyroscopeTags = []string{"service_version", "service_instance_id"}

// pyroscopeConfig maps the profiling config into a pyroscope.Config. The tag
// mapping and the transport wiring are both here so the whole SDK-facing surface
// is unit-testable from a config value; the live logger is attached by
// startProfiling.
//
// It is no longer side-effect free: it installs the health/TLS upload client
// (HTTPClient), which records into the process-wide profilingHealthState. That is
// deliberate — attaching it at the mapping means upload health cannot be lost by a
// caller forgetting to opt in, and there is exactly one profiler per process.
func pyroscopeConfig(cfg *config.Config, version string, opts ...profilingOption) pyroscope.Config {
	var po profilingOptions
	for _, o := range opts {
		o(&po)
	}
	p := cfg.Profiling.Pyroscope
	tags := map[string]string{
		"service_version":     version,
		"service_instance_id": instanceID(cfg),
	}
	for k, v := range p.Tags {
		if slices.Contains(reservedPyroscopeTags, k) {
			continue
		}
		tags[k] = v
	}
	topts := pyroscopeTransportOptionsFromConfig(p)
	pc := pyroscope.Config{
		ApplicationName:   serviceName,
		ServerAddress:     p.ServerAddress,
		BasicAuthUser:     p.BasicAuthUser,
		BasicAuthPassword: p.BasicAuthPassword.Reveal(),
		TenantID:          p.TenantID,
		Tags:              tags,
		ProfileTypes:      pyroscopeProfileTypes(cfg.Profiling),
	}
	// Reserved headers win: sanitizePyroscopeHeaders removes any colliding
	// operator header BEFORE the SDK sees the map, because the SDK applies
	// HTTPHeaders after the auth/tenant headers and would otherwise let a user
	// header overwrite identity. See sanitizePyroscopeHeaders for the full rule.
	if kept, _ := sanitizePyroscopeHeaders(topts.Headers, topts.BasicAuthSet, topts.TenantSet); len(kept) > 0 {
		pc.HTTPHeaders = kept
	}
	// A TLS material failure is reported by startProfiling rather than here: the
	// mapping has no logger, and a profiler that cannot upload is non-fatal.
	if client, err := newProfilingUploadClient(topts, profilingHealthState, po.emitter); err == nil {
		pc.HTTPClient = client
	}
	if d := p.UploadRate.D(); d > 0 {
		pc.UploadRate = d
	}
	return pc
}

// profilingOptions are the live dependencies startProfiling threads into the
// otherwise config-only mapping.
type profilingOptions struct {
	// emitter records the upload-health metrics. Nil withholds ONLY the five
	// tailscale2otel.profiling.upload.* metrics: the tracker still records
	// everything, so the admin status page is fully populated either way.
	emitter telemetry.Emitter
}

// profilingOption configures startProfiling / pyroscopeConfig.
type profilingOption func(*profilingOptions)

// withProfilingEmitter supplies the emitter for the Pyroscope upload-health
// metrics.
//
// WIRING HAND-OFF: it is variadic precisely so the existing app.New call site
// keeps compiling unchanged. Passing it from app.New is the one line that turns
// the upload-health METRICS on; the status page needs nothing.
func withProfilingEmitter(e telemetry.Emitter) profilingOption {
	return func(o *profilingOptions) { o.emitter = e }
}

// startProfiling applies the runtime mutex/block profiling rates (needed by both
// the Pyroscope push and the /debug/pprof pull paths) and, when Pyroscope push
// is enabled, starts the continuous profiler. It returns the profiler (nil when
// push is disabled) so the caller can Stop it on shutdown.
func startProfiling(cfg *config.Config, version string, logger *slog.Logger, opts ...profilingOption) (*pyroscope.Profiler, error) {
	prof := cfg.Profiling
	// Apply the process-global mutex/block sampling rates only when something
	// actually consumes them (the pprof pull path or the Pyroscope push). They are
	// on by default (config.Default sets the fractions), so gating here keeps a
	// process with all profiling disabled from paying the sampling overhead for
	// profiles nobody collects.
	if prof.Pprof.Enabled || prof.Pyroscope.Enabled {
		if prof.MutexProfileFraction > 0 {
			runtime.SetMutexProfileFraction(prof.MutexProfileFraction)
		}
		if prof.BlockProfileRate > 0 {
			runtime.SetBlockProfileRate(prof.BlockProfileRate)
		}
	}
	if !prof.Pyroscope.Enabled {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	topts := pyroscopeTransportOptionsFromConfig(prof.Pyroscope)
	// Surface unusable TLS material and dropped headers HERE rather than in the
	// mapping, which has no logger. Neither is fatal: without a usable client the
	// SDK falls back to its own (system roots, no client certificate), which is
	// the right failure mode for a diagnostic side-channel — but an operator who
	// configured a custom CA must be told it was not applied.
	if _, err := topts.tlsConfig(); err != nil {
		logger.Error("pyroscope TLS material unusable; uploading with default TLS settings", "error", err)
	}
	if _, dropped := sanitizePyroscopeHeaders(topts.Headers, topts.BasicAuthSet, topts.TenantSet); len(dropped) > 0 {
		// Names only. A dropped header's VALUE is exactly the kind of credential
		// this whole path is careful with.
		logger.Warn("ignoring reserved pyroscope headers; the built-in value wins",
			"headers", strings.Join(dropped, ","))
	}

	pc := pyroscopeConfig(cfg, version, opts...)
	// The SDK's logger formats server responses and its own configuration into
	// messages with no notion of which parts are credentials, so wrap it in the
	// secret redactor. The upload client already strips response BODIES before the
	// SDK can see them; this covers the extra-header values, which only we know.
	pc.Logger = pyroscopeLogger{l: logger, redact: redactSecretsFunc(topts.secretValues())}
	return pyroscope.Start(pc)
}

// pyroscopeLogger adapts *slog.Logger to the pyroscope.Logger interface, scrubbing
// known secret values out of every message. redact may be nil (no scrubbing).
type pyroscopeLogger struct {
	l      *slog.Logger
	redact func(string) string
}

func (p pyroscopeLogger) msg(format string, args ...any) string {
	s := fmt.Sprintf(format, args...)
	if p.redact != nil {
		s = p.redact(s)
	}
	return s
}

func (p pyroscopeLogger) Infof(format string, args ...any)  { p.l.Info(p.msg(format, args...)) }
func (p pyroscopeLogger) Debugf(format string, args ...any) { p.l.Debug(p.msg(format, args...)) }
func (p pyroscopeLogger) Errorf(format string, args ...any) { p.l.Error(p.msg(format, args...)) }
