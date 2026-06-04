package portservice

import "testing"

func TestLookupName_WellKnownPorts(t *testing.T) {
	cases := []struct {
		transport string
		port      uint16
		want      string
	}{
		{"tcp", 22, "ssh"},
		{"tcp", 80, "http"},
		{"tcp", 443, "https"},
		{"udp", 53, "domain"},
		{"tcp", 5432, "postgresql"},
		{"tcp", 3306, "mysql"},
	}
	for _, c := range cases {
		got, ok := LookupName(c.transport, c.port)
		if !ok {
			t.Errorf("LookupName(%q, %d) ok = false, want true", c.transport, c.port)
			continue
		}
		if got != c.want {
			t.Errorf("LookupName(%q, %d) = %q, want %q", c.transport, c.port, got, c.want)
		}
	}
}

func TestLookupName_Unknown(t *testing.T) {
	cases := []struct {
		name      string
		transport string
		port      uint16
	}{
		{"transport not loaded", "icmp", 443},
		{"port 0 is reserved with no service name", "tcp", 0},
	}
	for _, c := range cases {
		if got, ok := LookupName(c.transport, c.port); ok {
			t.Errorf("%s: LookupName(%q, %d) = (%q, true), want (\"\", false)", c.name, c.transport, c.port, got)
		}
	}
}

// TestTableSize guards against a corrupt or truncated embedded CSV: the IANA
// registry holds thousands of tcp/udp assignments with a service name.
func TestTableSize(t *testing.T) {
	LookupName("tcp", 22) // trigger the lazy load
	if n := len(table); n < 1000 {
		t.Errorf("parsed table has %d entries, want >= 1000 (embedded CSV may be truncated)", n)
	}
}
