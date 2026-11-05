package check

import "testing"

func TestParseSource(t *testing.T) {
	tests := []struct {
		in      string
		want    Source
		wantErr bool
	}{
		{in: "apps/deploy/frontend", want: Source{Namespace: "apps", Kind: KindDeployment, Name: "frontend"}},
		{in: "apps/deployment/frontend", want: Source{Namespace: "apps", Kind: KindDeployment, Name: "frontend"}},
		{in: "apps/cronjob/cleanup", want: Source{Namespace: "apps", Kind: KindCronJob, Name: "cleanup"}},
		{in: "apps/new-pod", want: Source{Namespace: "apps", Kind: KindNewPod}},
		{in: "apps", wantErr: true},
		{in: "apps/frontend", wantErr: true},
		{in: "apps/pod/frontend", wantErr: true},
		{in: "apps/deploy/", wantErr: true},
		{in: "/new-pod", wantErr: true},
		{in: "apps/deploy/frontend/extra", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseSource(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseSource(%q) error = %v, want error %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseSource(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in      string
		wantURL string
		wantErr bool
	}{
		{in: "apps/api/items", wantURL: "http://api.apps:8080/items"},
		{in: "apps/api", wantURL: "http://api.apps:8080/"},
		{in: "apps/api/", wantURL: "http://api.apps:8080/"},
		{in: "auth/login:8443/oauth/token", wantURL: "http://login.auth:8443/oauth/token"},
		{in: "apps", wantErr: true},
		{in: "/api/items", wantErr: true},
		{in: "apps//items", wantErr: true},
		{in: "apps/api:http/", wantErr: true},
		{in: "apps/:8080/", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseTarget(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseTarget(%q) error = %v, want error %v", tt.in, err, tt.wantErr)
			continue
		}
		if err == nil && got.URL() != tt.wantURL {
			t.Errorf("ParseTarget(%q).URL() = %q, want %q", tt.in, got.URL(), tt.wantURL)
		}
	}
}

func TestExpectMatches(t *testing.T) {
	tests := []struct {
		expect string
		answer string
		want   bool
	}{
		{expect: "2xx", answer: "200 OK", want: true},
		{expect: "2xx", answer: "204 No Content", want: true},
		{expect: "2xx", answer: "403 Forbidden", want: false},
		{expect: "2xx", answer: "error: context deadline exceeded", want: false},
		{expect: "200", answer: "200 OK", want: true},
		{expect: "200", answer: "201 Created", want: false},
		{expect: "403", answer: "403 Forbidden", want: true},
		{expect: "403", answer: "error: dial tcp: i/o timeout", want: false},
		// Blocked passes on an L7 deny (403) and an L3/L4 deny (no connection).
		{expect: "blocked", answer: "403 Forbidden", want: true},
		{expect: "blocked", answer: "error: dial tcp 10.96.0.12:8080: i/o timeout", want: true},
		{expect: "blocked", answer: "200 OK", want: false},
		{expect: "blocked", answer: "404 Not Found", want: false},
		{expect: "blocked", answer: "", want: false},
		{expect: "2xx", answer: "", want: false},
	}
	for _, tt := range tests {
		e, err := ParseExpect(tt.expect)
		if err != nil {
			t.Fatalf("ParseExpect(%q): %v", tt.expect, err)
		}
		if got := e.Matches(tt.answer); got != tt.want {
			t.Errorf("expect %s, answer %q: got %v, want %v", tt.expect, tt.answer, got, tt.want)
		}
	}
}

func TestParseExpectRejects(t *testing.T) {
	for _, in := range []string{"", "ok", "6xx", "0xx", "2XX", "99", "600", "allowed"} {
		if _, err := ParseExpect(in); err == nil {
			t.Errorf("ParseExpect(%q) should fail", in)
		}
	}
}

func TestParseTestServerTarget(t *testing.T) {
	tests := []struct {
		in       string
		wantPath string
	}{
		{in: "test-server", wantPath: "/"},
		{in: "test-server/", wantPath: "/"},
		{in: "test-server/items", wantPath: "/items"},
	}
	for _, tt := range tests {
		got, err := ParseTarget(tt.in)
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", tt.in, err)
		}
		if !got.TestServer || got.Path != tt.wantPath || got.Port != defaultPort {
			t.Errorf("ParseTarget(%q) = %+v, want the test server with path %q", tt.in, got, tt.wantPath)
		}
	}
	// A namespace that starts like it is still a namespace.
	if got, err := ParseTarget("test-servers/api/items"); err != nil || got.TestServer {
		t.Errorf("ParseTarget(test-servers/api/items) = %+v, %v, want a normal target", got, err)
	}
}
