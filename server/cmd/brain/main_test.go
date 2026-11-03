package main

import "testing"

func TestHealthcheckURL(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "default address",
			env:  map[string]string{},
			want: "http://127.0.0.1:8080/health",
		},
		{
			name: "wildcard bind address maps to loopback",
			env:  map[string]string{"BRAIN_ADDR": "0.0.0.0:9000"},
			want: "http://127.0.0.1:9000/health",
		},
		{
			name: "empty host maps to loopback",
			env:  map[string]string{"BRAIN_ADDR": ":9000"},
			want: "http://127.0.0.1:9000/health",
		},
		{
			name: "loopback address is kept",
			env:  map[string]string{"BRAIN_ADDR": "127.0.0.1:8081"},
			want: "http://127.0.0.1:8081/health",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			if got := healthcheckURL(getenv); got != tt.want {
				t.Errorf("healthcheckURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := run([]string{"unknown"}); err == nil {
		t.Fatal("run() error = nil, want error")
	}
}
