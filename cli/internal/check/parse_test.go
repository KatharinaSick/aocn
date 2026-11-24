package check

import (
	"strings"
	"testing"
)

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

func TestParseLogin(t *testing.T) {
	l, err := ParseLogin(LoginCheck{Issuer: "auth/idp/realms/apps", Client: "frontend", From: "apps/deploy/frontend", Method: "GET", To: "apps/api/items", Expect: "2xx"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Issuer.URL() != "http://idp.auth:8080/realms/apps" || l.Target == nil || l.Target.URL() != "http://api.apps:8080/items" {
		t.Errorf("got %+v", l)
	}
	if got := l.Describe(); got != "frontend's ServiceAccount as frontend → api GET /items" {
		t.Errorf("Describe() = %q", got)
	}

	only, err := ParseLogin(LoginCheck{Issuer: "auth/idp:8443/realms/apps", Client: "worker", Secret: "s3cret", From: "apps/new-pod", Expect: "blocked"})
	if err != nil {
		t.Fatal(err)
	}
	if only.Target != nil || only.Issuer.URL() != "http://idp.auth:8443/realms/apps" {
		t.Errorf("got %+v", only)
	}
	if got := only.Describe(); got != "a new pod in apps logging in as worker with a secret" {
		t.Errorf("Describe() = %q", got)
	}
}

func TestParseLoginRejects(t *testing.T) {
	ok := LoginCheck{Issuer: "auth/idp/realms/apps", Client: "frontend", From: "apps/new-pod", Expect: "2xx"}
	for name, change := range map[string]func(*LoginCheck){
		"bad issuer":     func(c *LoginCheck) { c.Issuer = "auth" },
		"test server":    func(c *LoginCheck) { c.Issuer = "test-server" },
		"no client":      func(c *LoginCheck) { c.Client = "" },
		"bad from":       func(c *LoginCheck) { c.From = "apps/frontend" },
		"bad expect":     func(c *LoginCheck) { c.Expect = "ok" },
		"method alone":   func(c *LoginCheck) { c.Method = "GET" },
		"to alone":       func(c *LoginCheck) { c.To = "apps/api/items" },
		"to test server": func(c *LoginCheck) { c.Method, c.To = "GET", "test-server" },
		"issuer slash":   func(c *LoginCheck) { c.Issuer = "auth/idp/realms/apps/" },
		"root slash":     func(c *LoginCheck) { c.Issuer = "auth/idp/" },
		"issuer query":   func(c *LoginCheck) { c.Issuer = "auth/idp/realms/apps?x=1" },
		"issuer anchor":  func(c *LoginCheck) { c.Issuer = "auth/idp/realms/apps#x" },
		"lower method":   func(c *LoginCheck) { c.Method, c.To = "get", "apps/api/items" },
		// A login alone answers with a token or a refusal, nothing else.
		"login 401": func(c *LoginCheck) { c.Expect = "401" },
		"login 4xx": func(c *LoginCheck) { c.Expect = "4xx" },
		"login 204": func(c *LoginCheck) { c.Expect = "204" },
		"login 3xx": func(c *LoginCheck) { c.Expect = "3xx" },
	} {
		t.Run(name, func(t *testing.T) {
			c := ok
			change(&c)
			if l, err := ParseLogin(c); err == nil {
				t.Errorf("got %+v, want an error", l)
			}
		})
	}
}

func TestLoginMatches(t *testing.T) {
	tests := []struct {
		expect string
		answer string
		want   bool
	}{
		// Only logging in.
		{expect: "2xx", answer: "200 OK", want: true},
		{expect: "2xx", answer: "login: 401 Unauthorized", want: false},
		{expect: "blocked", answer: "login: 401 Unauthorized", want: true},
		{expect: "blocked", answer: "login: 400 Bad Request", want: true},
		{expect: "blocked", answer: "200 OK", want: false},
		{expect: "401", answer: "login: 401 Unauthorized", want: false},
		// The issuer couldn't be reached: that's no answer about the workload at all.
		{expect: "blocked", answer: "login: error: dial tcp: i/o timeout", want: false},
		{expect: "2xx", answer: "login: error: dial tcp: i/o timeout", want: false},
		{expect: "blocked", answer: "login: 500 Internal Server Error", want: false},
		// The request after the login, like an HTTP check.
		{expect: "blocked", answer: "403 Forbidden", want: true},
		{expect: "blocked", answer: "error: dial tcp 10.96.0.12:8080: i/o timeout", want: true},
		{expect: "blocked", answer: "204 No Content", want: false},
		{expect: "2xx", answer: "200 OK", want: true},
		{expect: "404", answer: "404 Not Found", want: true},
		// Every 4xx at the login is a refusal, a 429 too.
		{expect: "blocked", answer: "login: 429 Too Many Requests", want: true},
		{expect: "blocked", answer: "login: nonsense", want: false},
		{expect: "3xx", answer: "301 Moved Permanently", want: true},
		{expect: "3xx", answer: "login: 401 Unauthorized", want: false},
	}
	for _, tt := range tests {
		e, err := ParseExpect(tt.expect)
		if err != nil {
			t.Fatal(err)
		}
		if got := (Login{Expect: e}).Matches(tt.answer); got != tt.want {
			t.Errorf("expect %s, answer %q: got %v, want %v", tt.expect, tt.answer, got, tt.want)
		}
	}
}

func TestTestPodArgs(t *testing.T) {
	probe := []string{"/station", "--role", "probe"}
	plain, err := testPodArgs("p-1", "apps", "station:1", "", probe, testPod{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plain, " ")
	// Without labels of its own, the pod only gets the probe's.
	for _, want := range []string{"--labels=aocn-verify=probe ", "--override-type=strategic", `"name":"p-1"`, `"readinessProbe":{"periodSeconds":60,"tcpSocket":{"port":1}}`, "-- /station --role probe --delay 2s"} {
		if !strings.Contains(joined+" ", want) {
			t.Errorf("args %s lack %s", joined, want)
		}
	}
	for _, unwanted := range []string{"serviceAccountName", "volumes"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("args without an account or token have %s: %s", unwanted, joined)
		}
	}

	login, err := testPodArgs("p-2", "apps", "station:1", "app=frontend", probe, testPod{Account: "frontend", TokenAudience: "http://idp.auth:8080/realms/apps"})
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(login, " ")
	for _, want := range []string{"--labels=app=frontend,aocn-verify=probe", `"serviceAccountName":"frontend"`, `"audience":"http://idp.auth:8080/realms/apps"`, `"name":"p-2"`, `"mountPath":"` + tokenDir + `"`, "readinessProbe"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %s lack %s", joined, want)
		}
	}
}

func TestLoginIssuerURL(t *testing.T) {
	for issuer, want := range map[string]string{
		"auth/idp":             "http://idp.auth:8080",
		"auth/idp:8443":        "http://idp.auth:8443",
		"auth/idp/realms/apps": "http://idp.auth:8080/realms/apps",
	} {
		l, err := ParseLogin(LoginCheck{Issuer: issuer, Client: "frontend", From: "apps/new-pod", Expect: "2xx"})
		if err != nil {
			t.Fatal(err)
		}
		if got := l.IssuerURL(); got != want {
			t.Errorf("IssuerURL() of %s = %q, want %q", issuer, got, want)
		}
	}
}
