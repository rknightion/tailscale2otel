package config

import (
	"strings"
	"testing"
	"time"
)

// Validation for the EPIC-04 (#480) config surface: per-class head sampling and
// remote-parent trust (#372/#373), Resource enrichment (#380), outbound
// credential reload (#362), and Pyroscope span profiles (#370).
//
// The shared theme is that each of these knobs has a shape that is accepted by
// YAML but inert or actively wrong at runtime — a class sampler naming a
// nonexistent strategy, a reserved key in resource.attributes, a poller with no
// interval, span profiles without tracing. Configured-but-inert is exactly what
// #305 stopped this repo tolerating, so each is refused at startup.

func TestValidate_TracingClassSamplers(t *testing.T) {
	t.Run("unknown class sampler", func(t *testing.T) {
		c := Default()
		c.Tracing.Enabled = true
		c.Tracing.Samplers.Receiver.Sampler = "sometimes"
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: unknown per-class sampler")
		}
		if !strings.Contains(err.Error(), "tracing.samplers.receiver.sampler") {
			t.Errorf("error %q should name the offending class key", err.Error())
		}
	})

	t.Run("class arg out of range", func(t *testing.T) {
		c := Default()
		c.Tracing.Enabled = true
		c.Tracing.Samplers.Scrape.Sampler = "traceidratio"
		c.Tracing.Samplers.Scrape.Arg = 1.5
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: per-class sampler arg outside [0,1]")
		}
		if !strings.Contains(err.Error(), "tracing.samplers.scrape.arg") {
			t.Errorf("error %q should name tracing.samplers.scrape.arg", err.Error())
		}
	})

	// An empty class sampler is the documented way to inherit the global one, so
	// it must not be mistaken for an invalid enum value.
	t.Run("empty class inherits", func(t *testing.T) {
		c := Default()
		c.Tracing.Enabled = true
		if err := c.Validate(); err != nil {
			t.Fatalf("default (all classes unset) rejected: %v", err)
		}
	})

	t.Run("valid class override accepted", func(t *testing.T) {
		c := Default()
		c.Tracing.Enabled = true
		c.Tracing.Samplers.Receiver.Sampler = "traceidratio"
		c.Tracing.Samplers.Receiver.Arg = 0.01
		c.Tracing.Samplers.Background.Sampler = "always_on"
		if err := c.Validate(); err != nil {
			t.Fatalf("valid per-class overrides rejected: %v", err)
		}
	})
}

func TestValidate_TracingRemoteParent(t *testing.T) {
	t.Run("unknown policy", func(t *testing.T) {
		c := Default()
		c.Tracing.RemoteParent = "maybe"
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: unknown remote_parent policy")
		}
		if !strings.Contains(err.Error(), "trust") || !strings.Contains(err.Error(), "link") {
			t.Errorf("error %q should list the accepted policies", err.Error())
		}
	})

	for _, p := range []string{"trust", "ignore", "link", ""} {
		t.Run("accepts "+p, func(t *testing.T) {
			c := Default()
			c.Tracing.RemoteParent = p
			if err := c.Validate(); err != nil {
				t.Fatalf("remote_parent %q rejected: %v", p, err)
			}
		})
	}
}

func TestValidate_ResourceEnrichment(t *testing.T) {
	// The app's own identity and the two signal-scoped attributes are the whole
	// point of the guard: accepting them here would either be silently ignored by
	// the telemetry layer (inert config) or would break the #187 metrics-Resource
	// contract and roadmap item L. Refusing at startup says which.
	for _, key := range []string{
		"service.name", "service.version", "service.instance.id",
		"tailscale.tailnet", "tailscale2otel.provider",
	} {
		t.Run("reserved key "+key, func(t *testing.T) {
			c := Default()
			c.Resource.Attributes = map[string]string{key: "x"}
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error: reserved resource attribute %q", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q should name the reserved key", err.Error())
			}
		})
	}

	t.Run("empty key", func(t *testing.T) {
		c := Default()
		c.Resource.Attributes = map[string]string{"": "x"}
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: empty resource attribute key")
		}
		if !strings.Contains(err.Error(), "resource.attributes") {
			t.Errorf("error %q should name resource.attributes", err.Error())
		}
	})

	t.Run("oversized service_namespace", func(t *testing.T) {
		c := Default()
		c.Resource.ServiceNamespace = strings.Repeat("n", 257)
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: oversized service_namespace")
		}
		if !strings.Contains(err.Error(), "resource.service_namespace") {
			t.Errorf("error %q should name resource.service_namespace", err.Error())
		}
	})

	t.Run("valid enrichment accepted", func(t *testing.T) {
		c := Default()
		c.Resource.ServiceNamespace = "netops"
		c.Resource.DeploymentEnvironment = "prod"
		c.Resource.Attributes = map[string]string{"deploy.team": "platform"}
		c.Resource.FromEnv = true
		if err := c.Validate(); err != nil {
			t.Fatalf("valid enrichment rejected: %v", err)
		}
	})

	t.Run("zero value accepted", func(t *testing.T) {
		if err := Default().Validate(); err != nil {
			t.Fatalf("default (no enrichment) rejected: %v", err)
		}
	})
}

func TestValidate_CredentialReload(t *testing.T) {
	// A poller with a non-positive interval is the inert shape: enabled reads as
	// "rotation is handled" while nothing ever polls.
	t.Run("otlp poller needs an interval", func(t *testing.T) {
		c := Default()
		c.OTLP.CredentialReload.Enabled = true
		c.OTLP.CredentialReload.Interval = Duration(0)
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: credential_reload enabled with no interval")
		}
		if !strings.Contains(err.Error(), "otlp.credential_reload.interval") {
			t.Errorf("error %q should name otlp.credential_reload.interval", err.Error())
		}
	})

	t.Run("pyroscope poller needs an interval", func(t *testing.T) {
		c := Default()
		c.Profiling.Pyroscope.CredentialReload.Enabled = true
		c.Profiling.Pyroscope.CredentialReload.Interval = Duration(-1)
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: pyroscope credential_reload enabled with no interval")
		}
		if !strings.Contains(err.Error(), "profiling.pyroscope.credential_reload.interval") {
			t.Errorf("error %q should name the pyroscope interval key", err.Error())
		}
	})

	// A very short interval turns rotation support into a stat() loop, so there is
	// a floor rather than only a positivity check.
	t.Run("interval floor", func(t *testing.T) {
		c := Default()
		c.OTLP.CredentialReload.Enabled = true
		c.OTLP.CredentialReload.Interval = Duration(time.Second)
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: credential_reload interval below the floor")
		}
		if !strings.Contains(err.Error(), "5s") {
			t.Errorf("error %q should state the floor", err.Error())
		}
	})

	// Disabled means "no poller", not "misconfigured", so a zero interval there is
	// legitimate — Reload() can still be driven explicitly.
	t.Run("disabled ignores the interval", func(t *testing.T) {
		c := Default()
		c.OTLP.CredentialReload.Enabled = false
		c.OTLP.CredentialReload.Interval = Duration(0)
		if err := c.Validate(); err != nil {
			t.Fatalf("disabled credential_reload with a zero interval rejected: %v", err)
		}
	})

	t.Run("valid poller accepted", func(t *testing.T) {
		c := Default()
		c.OTLP.CredentialReload.Enabled = true
		c.Profiling.Pyroscope.CredentialReload.Enabled = true
		if err := c.Validate(); err != nil {
			t.Fatalf("default 30s interval rejected: %v", err)
		}
	})
}

func TestValidate_SpanProfiles(t *testing.T) {
	// Span profiles are a bridge between two independently-optional subsystems.
	// Enabling it with either half off produces no correlation at all, silently.
	base := func() *Config {
		c := Default()
		c.Profiling.Pyroscope.Enabled = true
		c.Profiling.Pyroscope.ServerAddress = "https://profiles.example.com"
		c.Tracing.Enabled = true
		c.Profiling.Pyroscope.SpanProfiles.Enabled = true
		return c
	}

	t.Run("requires tracing", func(t *testing.T) {
		c := base()
		c.Tracing.Enabled = false
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: span_profiles without tracing.enabled")
		}
		if !strings.Contains(err.Error(), "tracing.enabled") {
			t.Errorf("error %q should name tracing.enabled", err.Error())
		}
	})

	t.Run("requires pyroscope", func(t *testing.T) {
		c := base()
		c.Profiling.Pyroscope.Enabled = false
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: span_profiles without profiling.pyroscope.enabled")
		}
		if !strings.Contains(err.Error(), "profiling.pyroscope.enabled") {
			t.Errorf("error %q should name profiling.pyroscope.enabled", err.Error())
		}
	})

	t.Run("both enabled accepted", func(t *testing.T) {
		if err := base().Validate(); err != nil {
			t.Fatalf("span_profiles with both halves enabled rejected: %v", err)
		}
	})

	t.Run("off by default", func(t *testing.T) {
		if Default().Profiling.Pyroscope.SpanProfiles.Enabled {
			t.Error("span_profiles must be opt-in")
		}
	})
}
