package config

import (
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that unmarshals from YAML duration strings such
// as "30s", "5m", or "168h" via time.ParseDuration.
type Duration time.Duration

// UnmarshalYAML decodes a YAML scalar string into a Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// D returns the underlying time.Duration.
func (d Duration) D() time.Duration {
	return time.Duration(d)
}
