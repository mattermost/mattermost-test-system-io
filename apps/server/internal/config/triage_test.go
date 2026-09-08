package config

import "testing"

func TestTriageConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(*Config)
		wantErr bool
	}{
		{"optional integrations disabled", func(*Config) {}, false},
		{"OIDC without audience", func(c *Config) {
			c.TriageWorkflowRefs = []string{"owner/repo/.github/workflows/triage.yml@refs/heads/master"}
		}, true},
		{"enabled Jira without credentials", func(c *Config) { c.JiraEnabled = true }, true},
		{"negative cap", func(c *Config) { c.TriageQuarantineCap = -1 }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{LogFormat: "json", MaxUploadBytes: 1, MaxArtifactBytes: 1}
			tc.setup(&c)
			if err := c.validate(); (err != nil) != tc.wantErr {
				t.Fatalf("validate()=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
