package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// TestLoadSettingsWarnsInvalidAllowlistEntries covers RV1-18: LoadSettings
// logs one warning per allowlist entry that is not a valid host name, and
// none for valid entries.
func TestLoadSettingsWarnsInvalidAllowlistEntries(t *testing.T) {
	logs := &ac19CapturingLogger{}
	orig := settingsLogger
	settingsLogger = logs
	t.Cleanup(func() { settingsLogger = orig })

	data := `{"allowedDomains":[
		{"domain":"example.com"},
		{"domain":"Sub.Example.org."},
		{"domain":"https://example.com"},
		{"domain":"example.com:8443"},
		{"domain":""},
		{"domain":"bad host"}
	]}`
	if _, err := LoadSettings(backend.AppInstanceSettings{JSONData: []byte(data)}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range logs.warnEntries() {
		got = append(got, w.kv["domain"].(string))
	}
	want := []string{"https://example.com", "example.com:8443", "", "bad host"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("warned about %q, want %q", got, want)
	}
}

// TestCheckHealthReportsAllowlistState covers RV1-18: CheckHealth reports an
// empty allowlist (OK, the proxy denies everything) and invalid entries
// (error, naming them), with the counts in JSONDetails.
func TestCheckHealthReportsAllowlistState(t *testing.T) {
	// Keep the expected LoadSettings warnings out of the test output.
	orig := settingsLogger
	settingsLogger = &ac19CapturingLogger{}
	t.Cleanup(func() { settingsLogger = orig })

	cases := []struct {
		name        string
		jsonData    string
		wantStatus  backend.HealthStatus
		wantMessage []string
		wantDetails healthDetails
	}{
		{
			name:        "empty allowlist",
			jsonData:    ``,
			wantStatus:  backend.HealthStatusOk,
			wantMessage: []string{"allowlist is empty", "denies every request"},
			wantDetails: healthDetails{AllowlistEntries: 0},
		},
		{
			name:        "valid entries",
			jsonData:    `{"allowedDomains":[{"domain":"example.com"},{"domain":"example.org"}]}`,
			wantStatus:  backend.HealthStatusOk,
			wantMessage: []string{"allowlist entries: 2"},
			wantDetails: healthDetails{AllowlistEntries: 2},
		},
		{
			name:        "some invalid entries",
			jsonData:    `{"allowedDomains":[{"domain":"example.com"},{"domain":"https://bad.example"}]}`,
			wantStatus:  backend.HealthStatusError,
			wantMessage: []string{"1 of 2 allowlist entries", `"https://bad.example"`, "bare host name"},
			wantDetails: healthDetails{AllowlistEntries: 2, InvalidEntries: []string{"https://bad.example"}},
		},
		{
			name:        "only invalid entries",
			jsonData:    `{"allowedDomains":[{"domain":"example.com:8443"}]}`,
			wantStatus:  backend.HealthStatusError,
			wantMessage: []string{"1 of 1", "denies every request"},
			wantDetails: healthDetails{AllowlistEntries: 1, InvalidEntries: []string{"example.com:8443"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := tc1NewApp(t, tc.jsonData)
			res, err := app.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != tc.wantStatus {
				t.Errorf("status = %v, want %v (message %q)", res.Status, tc.wantStatus, res.Message)
			}
			for _, part := range tc.wantMessage {
				if !strings.Contains(res.Message, part) {
					t.Errorf("message %q does not contain %q", res.Message, part)
				}
			}
			var details healthDetails
			if err := json.Unmarshal(res.JSONDetails, &details); err != nil {
				t.Fatalf("JSONDetails %q: %v", res.JSONDetails, err)
			}
			if details.AllowlistEntries != tc.wantDetails.AllowlistEntries ||
				strings.Join(details.InvalidEntries, "|") != strings.Join(tc.wantDetails.InvalidEntries, "|") {
				t.Errorf("details = %+v, want %+v", details, tc.wantDetails)
			}
		})
	}
}
