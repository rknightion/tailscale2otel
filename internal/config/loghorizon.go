package config

import "fmt"

func (c *Config) logHorizonWarnings() []string {
	age := c.Delivery.LogMaxRecordAge
	if age <= 0 {
		return nil
	}
	var warnings []string
	check := func(key string, lookback Duration) {
		if lookback > age {
			warnings = append(warnings, fmt.Sprintf("%s exceeds delivery.log_max_record_age (%v) and will be clamped", key, age.D()))
		}
	}
	for _, spec := range []struct {
		name, source string
		enabled      bool
		lookback     Duration
		object       ObjectStoreConfig
	}{
		{"flowlogs", c.Collectors.Flowlogs.Source, c.Collectors.Flowlogs.Enabled, c.Collectors.Flowlogs.InitialLookback, c.Collectors.Flowlogs.ObjectStore},
		{"auditlogs", c.Collectors.Auditlogs.Source, c.Collectors.Auditlogs.Enabled, c.Collectors.Auditlogs.InitialLookback, c.Collectors.Auditlogs.ObjectStore},
	} {
		if !spec.enabled {
			continue
		}
		if pollsSource(spec.source) {
			check("collectors."+spec.name+".initial_lookback", spec.lookback)
		}
		if objectStoreSource(spec.source) {
			if len(c.Tailnets) == 0 {
				check("collectors."+spec.name+".objectstore.initial_lookback", spec.object.InitialLookback)
			}
			for i, t := range c.Tailnets {
				obj := t.ObjectStore.Flow
				if spec.name == "auditlogs" {
					obj = t.ObjectStore.Audit
				}
				check(fmt.Sprintf("tailnets[%d].objectstore.%s.initial_lookback", i, map[string]string{"flowlogs": "flow", "auditlogs": "audit"}[spec.name]), obj.InitialLookback)
			}
		}
	}
	if c.Collectors.K8sAudit.Enabled {
		if len(c.Tailnets) == 0 {
			check("collectors.k8s_audit.objectstore.initial_lookback", c.Collectors.K8sAudit.ObjectStore.InitialLookback)
		}
		for i, t := range c.Tailnets {
			check(fmt.Sprintf("tailnets[%d].objectstore.k8s_audit.initial_lookback", i), t.ObjectStore.K8sAudit.InitialLookback)
		}
	}
	return warnings
}
