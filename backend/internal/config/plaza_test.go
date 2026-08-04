package config

import "testing"

func TestPlazaConfigValidate(t *testing.T) {
	const secret = "shared-with-sub2api"
	cases := []struct {
		name    string
		cfg     PlazaConfig
		wantErr bool
		wantURL string
	}{
		{
			name: "disabled skips validation",
			cfg:  PlazaConfig{Enabled: false, Sub2apiBaseURL: "not a url"},
		},
		{
			name:    "enabled requires base url",
			cfg:     PlazaConfig{Enabled: true, Sub2apiJWTSecret: secret},
			wantErr: true,
		},
		{
			name:    "enabled requires jwt secret",
			cfg:     PlazaConfig{Enabled: true, Sub2apiBaseURL: "https://sub.example.com"},
			wantErr: true,
		},
		{
			name:    "rejects relative url",
			cfg:     PlazaConfig{Enabled: true, Sub2apiBaseURL: "/api", Sub2apiJWTSecret: secret},
			wantErr: true,
		},
		{
			name:    "rejects non-http scheme",
			cfg:     PlazaConfig{Enabled: true, Sub2apiBaseURL: "ftp://example.com", Sub2apiJWTSecret: secret},
			wantErr: true,
		},
		{
			name:    "normalizes to origin",
			cfg:     PlazaConfig{Enabled: true, Sub2apiBaseURL: "https://sub.example.com/some/path?a=1#frag", Sub2apiJWTSecret: secret},
			wantURL: "https://sub.example.com",
		},
		{
			name:    "strips trailing slash",
			cfg:     PlazaConfig{Enabled: true, Sub2apiBaseURL: "https://sub.example.com/", Sub2apiJWTSecret: secret},
			wantURL: "https://sub.example.com",
		},
		{
			name:    "keeps port",
			cfg:     PlazaConfig{Enabled: true, Sub2apiBaseURL: "http://localhost:8080", Sub2apiJWTSecret: secret},
			wantURL: "http://localhost:8080",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			err := cfg.validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantURL != "" && cfg.Sub2apiBaseURL != tt.wantURL {
				t.Errorf("Sub2apiBaseURL = %q, want %q", cfg.Sub2apiBaseURL, tt.wantURL)
			}
		})
	}
}
