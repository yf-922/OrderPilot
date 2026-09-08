package common

import "testing"

func TestValidRequestID(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"empty", "", false},
		{"valid", "trace-123_abc.xyz", true},
		{"too long", string(make([]byte, 65)), false},
		{"control character", "trace\n123", false},
		{"unicode", "请求-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validRequestID(tt.value); got != tt.want {
				t.Fatalf("validRequestID(%q)=%v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
