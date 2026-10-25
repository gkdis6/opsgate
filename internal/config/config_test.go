package config

import "testing"

func TestValidateCommandPrefix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix []string
		valid  bool
	}{
		{name: "empty", valid: true},
		{name: "absolute", prefix: []string{"/usr/bin/sudo"}, valid: true},
		{name: "flags after executable", prefix: []string{"/usr/bin/sudo", "-n"}, valid: true},
		{name: "paths after executable", prefix: []string{"/usr/bin/env", "/usr/bin/sudo", "-n"}, valid: true},
		{name: "flag first", prefix: []string{"-n"}},
		{name: "flag before executable", prefix: []string{"-n", "/usr/bin/sudo"}},
		{name: "relative executable", prefix: []string{"sudo", "-n"}},
		{name: "empty executable", prefix: []string{"", "-n"}},
		{name: "relative argument", prefix: []string{"/usr/bin/sudo", "sudo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Providers.Host.CommandPrefix = tc.prefix
			if err := cfg.Validate(); (err == nil) != tc.valid {
				t.Errorf("Validate() = %v, valid = %v", err, tc.valid)
			}
		})
	}
}
