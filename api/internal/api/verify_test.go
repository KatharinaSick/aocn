package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/doors"
	"github.com/katharinasick/aocn/api/internal/results"
)

// withAuthorization sends a request with this Authorization header (none if empty).
func withAuthorization(h http.Handler, method, path, authorization, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	h.ServeHTTP(rec, req)
	return rec
}

// verifyAs sends results the way aocn verify does.
func verifyAs(h http.Handler, door, token string, sent []results.CheckResult) *httptest.ResponseRecorder {
	body, _ := json.Marshal(verifyRequest{Results: sent})
	auth := ""
	if token != "" {
		auth = "Bearer " + token
	}
	return withAuthorization(h, http.MethodPost, "/doors/"+door+"/verify", auth, string(body))
}

// door1Checks are door 1's checks as the API hands them out, IDs included.
func door1Checks(t *testing.T) []doors.Check {
	t.Helper()
	checks, err := doors.ParseChecks([]byte(uploaded["doors/01/checks.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	return checks
}

// resultsFor answers every check, passed or not.
func resultsFor(checks []doors.Check, passed bool) []results.CheckResult {
	var sent []results.CheckResult
	for _, c := range checks {
		sent = append(sent, results.CheckResult{ID: c.ID, Passed: passed, Got: "200 OK"})
	}
	return sent
}

func TestVerifyRecordsEveryRun(t *testing.T) {
	now := door1OpensAt.Add(time.Hour)
	h := doorsAt(t, now, uploaded)
	checks := door1Checks(t)

	for _, passed := range []bool{false, true} {
		rec := verifyAs(h, "1", playerToken, resultsFor(checks, passed))
		var body verifyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("status %d, %v: %s", rec.Code, err, rec.Body.String())
		}
		if body.Passed != passed || body.Login != "player" || body.Door != 1 {
			t.Errorf("got %+v", body)
		}
	}

	if len(h.results.recorded) != 2 {
		t.Fatalf("recorded %d runs, want 2", len(h.results.recorded))
	}
	r := h.results.recorded[1]
	if r.user != (results.User{GitHubID: 8, Login: "player", AvatarURL: "https://example.com/8.png"}) {
		t.Errorf("user: %+v", r.user)
	}
	if r.result.Door != 1 || !r.result.Passed || !r.result.At.Equal(now) || len(r.result.Checks) != len(checks) {
		t.Errorf("result: %+v", r.result)
	}
}

// One failed check fails the door, whatever else the request says.
func TestVerifyDecidesPassedItself(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	sent := resultsFor(door1Checks(t), true)
	sent[1].Passed = false
	verifyAs(h, "1", playerToken, sent)
	if len(h.results.recorded) != 1 || h.results.recorded[0].result.Passed {
		t.Errorf("recorded %+v, want one failed run", h.results.recorded)
	}
}

func TestVerifyNeedsSignIn(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	sent := resultsFor(door1Checks(t), true)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"signed out": verifyAs(h, "1", "", sent),
		"bad token":  verifyAs(h, "1", "made-up", sent),
	} {
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
	}
	if len(h.results.recorded) != 0 {
		t.Errorf("recorded %+v", h.results.recorded)
	}
}

// Results for a closed door are only kept from previewers: nobody else can have played it.
func TestVerifyClosedDoor(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)
	sent := resultsFor(door1Checks(t), true)

	rec := verifyAs(h, "1", playerToken, sent)
	if rec.Code != http.StatusNotFound {
		t.Errorf("player: status %d, want 404", rec.Code)
	}
	assertNoSecrets(t, "player", rec)
	if len(h.results.recorded) != 0 {
		t.Fatalf("recorded a player's result for a closed door")
	}

	// Previewers of another door are players here.
	if rec := verifyAs(h, "1", door2PreviewerToken, sent); rec.Code != http.StatusNotFound {
		t.Errorf("door 2 previewer: status %d, want 404", rec.Code)
	}
	if len(h.results.recorded) != 0 {
		t.Fatalf("recorded a door 2 previewer's result for door 1")
	}

	if rec := verifyAs(h, "1", previewerToken, sent); rec.Code != http.StatusOK {
		t.Errorf("previewer: status %d, want 200", rec.Code)
	}
	if len(h.results.recorded) != 1 || h.results.recorded[0].user.GitHubID != previewer.ID {
		t.Errorf("recorded %+v, want the previewer's run", h.results.recorded)
	}
}

// The results must be exactly the door's checks: none missing, none made up, none twice, and
// none for a check that was edited.
func TestVerifyRefusesResultsThatArentTheChecks(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	checks := door1Checks(t)
	all := resultsFor(checks, true)

	edited, err := doors.ParseChecks([]byte("- ready: [first-ns]\n- job: first-ns/cronjob/another-job\n"))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]results.CheckResult{
		"none":      nil,
		"missing":   all[:1],
		"made up":   append(append([]results.CheckResult{}, all...), results.CheckResult{ID: "0123456789abcdef", Passed: true}),
		"twice":     {all[0], all[0]},
		"edited":    resultsFor(edited, true),
		"other ids": {{ID: "a", Passed: true}, {ID: "b", Passed: true}},
	}
	for name, sent := range tests {
		if rec := verifyAs(h, "1", playerToken, sent); rec.Code != http.StatusConflict {
			t.Errorf("%s: status %d, want 409", name, rec.Code)
		}
	}
	if len(h.results.recorded) != 0 {
		t.Errorf("recorded %+v", h.results.recorded)
	}
}

func TestVerifyRefusesBrokenRequests(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	for name, body := range map[string]string{
		"not json":      "passed",
		"unknown field": `{"results": [], "passed": true}`,
		"too big":       `{"results": [{"id": "` + strings.Repeat("a", maxVerifyBytes) + `"}]}`,
	} {
		rec := withAuthorization(h, http.MethodPost, "/doors/1/verify", "Bearer "+playerToken, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

func TestVerifyWhenSavingFails(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	h.results.err = errors.New("database is down")
	if rec := verifyAs(h, "1", playerToken, resultsFor(door1Checks(t), true)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}

func TestMatchResultsKeepsTheChecksOrderAndShortensAnswers(t *testing.T) {
	checks := door1Checks(t)
	long := strings.Repeat("é", maxGotRunes+10)
	sent := []results.CheckResult{
		{ID: checks[1].ID, Passed: true, Got: long},
		{ID: checks[0].ID, Passed: true},
	}
	ordered, passed, err := matchResults(checks, sent)
	if err != nil || !passed {
		t.Fatalf("passed %v, err %v", passed, err)
	}
	if ordered[0].ID != checks[0].ID || ordered[1].ID != checks[1].ID {
		t.Errorf("order: %+v", ordered)
	}
	if got := []rune(ordered[1].Got); len(got) != maxGotRunes {
		t.Errorf("got kept %d runes, want %d", len(got), maxGotRunes)
	}
}
