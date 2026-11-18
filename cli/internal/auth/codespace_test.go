package auth

import "testing"

func TestCodespaceToken(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"in a Codespace", map[string]string{"CODESPACES": "true", "GITHUB_TOKEN": "ghu_x"}, "ghu_x"},
		{"elsewhere, with a token", map[string]string{"GITHUB_TOKEN": "ghp_x"}, ""},
		{"in a Codespace without a token", map[string]string{"CODESPACES": "true"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			if got := CodespaceToken(getenv); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
