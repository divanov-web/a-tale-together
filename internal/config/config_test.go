package config

import "testing"

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "defaults without token fail", mutate: func(c *Config) {}, wantErr: true},
		{name: "token present passes", mutate: func(c *Config) { c.TelegramBotToken = "x" }, wantErr: false},
		{name: "bad log level fails", mutate: func(c *Config) { c.TelegramBotToken = "x"; c.LogLevel = "loud" }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := Defaults()
			tt.mutate(&cfg)
			if err := cfg.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
