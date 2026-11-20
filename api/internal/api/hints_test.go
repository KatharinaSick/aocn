package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/results"
)

// hintsAs asks for the player's hints, the way aocn hint does.
func hintsAs(t *testing.T, h http.Handler, door, token string) hintsResponse {
	t.Helper()
	rec := getAs(h, "/doors/"+door+"/hints", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("hints: status %d, %s", rec.Code, rec.Body.String())
	}
	var body hintsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// openHint opens a hint, the way aocn hint does once the player said yes.
func openHint(h http.Handler, door, token, body string) *httptest.ResponseRecorder {
	return withAuthorization(h, http.MethodPost, "/doors/"+door+"/hints/next", "Bearer "+token, body)
}

func readOpened(t *testing.T, rec *httptest.ResponseRecorder) openedHintResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("open: status %d, %s", rec.Code, rec.Body.String())
	}
	var body openedHintResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// A player only ever gets the hints they opened, one at a time, in order, and each costs
// once.
func TestHintsOpenOneAtATime(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(time.Hour), uploaded)

	got := hintsAs(t, h, "1", playerToken)
	if got.Door != 1 || got.Total != 3 || len(got.Hints) != 0 || got.Next == nil || *got.Next != (nextHint{Hint: 1, Cost: 5}) || got.Fixed || got.Preview {
		t.Fatalf("before any hint: %+v", got)
	}
	if body := getAs(h, "/doors/1/hints", playerToken).Body.String(); strings.Contains(body, "-hint") {
		t.Errorf("before any hint: %s", body)
	}

	opened := readOpened(t, openHint(h, "1", playerToken, `{"hint": 1, "cost": 5}`))
	if opened.Cost != 5 || !slices.Equal(opened.Hints, []string{"first-hint"}) || *opened.Next != (nextHint{Hint: 2, Cost: 15}) {
		t.Errorf("first: %+v", opened)
	}
	// Again: free, and nothing more.
	opened = readOpened(t, openHint(h, "1", playerToken, `{"hint": 1, "cost": 5}`))
	if opened.Cost != 0 || len(opened.Hints) != 1 {
		t.Errorf("first again: %+v", opened)
	}
	// Skipping ahead is refused, and gives nothing away.
	rec := openHint(h, "1", playerToken, `{"hint": 3, "cost": 30}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("third before second: status %d", rec.Code)
	}
	for _, secret := range []string{"second-hint", "third-hint"} {
		if body := rec.Body.String(); strings.Contains(body, secret) {
			t.Errorf("third before second gave away %q: %s", secret, body)
		}
	}

	opened = readOpened(t, openHint(h, "1", playerToken, `{"hint": 2, "cost": 15}`))
	if opened.Cost != 15 || *opened.Next != (nextHint{Hint: 3, Cost: 30}) {
		t.Errorf("second: %+v", opened)
	}
	opened = readOpened(t, openHint(h, "1", playerToken, `{"hint": 3, "cost": 30}`))
	if opened.Cost != 30 || opened.Next != nil || !slices.Equal(opened.Hints, []string{"first-hint", "second-hint", "third-hint"}) {
		t.Errorf("third: %+v", opened)
	}
	if len(h.results.hints) != 3 {
		t.Errorf("recorded %+v", h.results.hints)
	}

	// Someone else still has none.
	if other := hintsAs(t, h, "1", previewerToken); len(other.Hints) != 0 {
		t.Errorf("previewer: %+v", other)
	}
}

// errDown is the database, down.
var errDown = errors.New("database is down")

func TestOpenHintRefusesNonsense(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	for _, body := range []string{``, `{}`, `{"hint": 1}`, `{"hint": 0}`, `{"hint": 4}`, `{"hint": -1}`, `{"hint": "1"}`, `{"hint": 1, "free": true}`, `[1]`} {
		if rec := openHint(h, "1", playerToken, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", body, rec.Code)
		}
	}
	if len(h.results.hints) != 0 {
		t.Errorf("opened %+v", h.results.hints)
	}
}

// Hints of a closed door are a door's content like any other: only for its previewers, who
// pay for them like everyone.
func TestHintsOfAClosedDoor(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)
	for _, token := range []string{playerToken, door3PreviewerToken} {
		rec := openHint(h, "1", token, `{"hint": 1, "cost": 0}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404", rec.Code)
		}
		assertNoSecrets(t, "closed door", rec)
	}
	opened := readOpened(t, openHint(h, "1", previewerToken, `{"hint": 1, "cost": 5}`))
	if opened.Cost != 5 || !opened.Preview || opened.Next.Cost != 15 {
		t.Errorf("previewer: %+v", opened)
	}
}

// Once a door is fixed, its hints are free.
func TestHintsAfterTheFixAreFree(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(time.Hour), uploaded)
	verifyAs(h, "1", playerToken, resultsFor(door1Checks(t), true))
	got := hintsAs(t, h, "1", playerToken)
	if !got.Fixed || got.Next.Cost != 0 {
		t.Errorf("got %+v", got)
	}
	if opened := readOpened(t, openHint(h, "1", playerToken, `{"hint": 1, "cost": 0}`)); opened.Cost != 0 {
		t.Errorf("opened: %+v", opened)
	}
}

// A hint never costs what the player didn't agree to: asked for at one price, it's refused at
// another. A door opening while a previewer was asked is how that happens.
func TestOpenHintRefusesAnotherPrice(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(time.Hour), uploaded)
	rec := openHint(h, "1", playerToken, `{"hint": 1, "cost": 0}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "costs 5 points now") {
		t.Errorf("status %d, %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "-hint") {
		t.Errorf("another price gave a hint away: %s", body)
	}
	if len(h.results.hints) != 0 {
		t.Errorf("opened %+v", h.results.hints)
	}
	if opened := readOpened(t, openHint(h, "1", playerToken, `{"hint": 1, "cost": 5}`)); opened.Cost != 5 {
		t.Errorf("at its price: %+v", opened)
	}
	// One that's open already is free whatever was agreed to: nothing is spent.
	if opened := readOpened(t, openHint(h, "1", playerToken, `{"hint": 1, "cost": 30}`)); opened.Cost != 0 {
		t.Errorf("again: %+v", opened)
	}
}

func TestOpenHintNeedsTheAccountAndTheDatabase(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	if rec := openHint(h, "1", cliToken(404), `{"hint": 1, "cost": 5}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown account: status %d, want 401", rec.Code)
	}
	h.results.err = errDown
	if rec := openHint(h, "1", playerToken, `{"hint": 1, "cost": 5}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("database down: status %d, want 503", rec.Code)
	}
	if rec := getAs(h, "/doors/1/hints", playerToken); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("database down, list: status %d, want 503", rec.Code)
	}
}

// After a pass, verify says what the door is worth.
func TestVerifyAnswersWithPoints(t *testing.T) {
	now := door1OpensAt.Add(time.Hour)
	h := doorsAt(t, now, uploaded)
	checks := door1Checks(t)

	// Right after aocn open: the workloads aren't up. That run doesn't count.
	notUp := []results.CheckResult{{ID: checks[0].ID, Got: "0/1 ready"}, {ID: checks[1].ID, Got: "not run"}}
	rec := verifyAs(h, "1", playerToken, notUp)
	var body verifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Passed || body.Score != nil {
		t.Fatalf("not up: %+v, %v", body, err)
	}
	if !h.results.recorded[0].result.NotReady {
		t.Errorf("not up: recorded %+v", h.results.recorded[0].result)
	}
	*h.clock = now.Add(time.Minute)
	// Up, but broken: that one counts.
	broken := []results.CheckResult{{ID: checks[0].ID, Passed: true}, {ID: checks[1].ID, Got: "failed"}}
	verifyAs(h, "1", playerToken, broken)
	if h.results.recorded[1].result.NotReady {
		t.Errorf("broken: recorded %+v", h.results.recorded[1].result)
	}
	*h.clock = now.Add(2 * time.Minute)
	readOpened(t, openHint(h, "1", playerToken, `{"hint": 1, "cost": 5}`))

	*h.clock = now.Add(3 * time.Minute)
	rec = verifyAs(h, "1", playerToken, resultsFor(checks, true))
	body = verifyResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Passed {
		t.Fatalf("pass: %s, %v", rec.Body.String(), err)
	}
	if body.Score == nil || *body.Score != (doorScore{Points: 95, Hints: 1, FailedVerifies: 1}) {
		t.Errorf("pass: score %+v", body.Score)
	}

	// Later passes don't change it.
	*h.clock = now.Add(4 * time.Minute)
	readOpened(t, openHint(h, "1", playerToken, `{"hint": 2, "cost": 0}`))
	rec = verifyAs(h, "1", playerToken, resultsFor(checks, true))
	body = verifyResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Score == nil || body.Score.Points != 95 {
		t.Errorf("again: score %+v", body.Score)
	}
}

// A previewer's pass before the opening is a fix once it opens, and their hints cost.
func TestVerifyPreviewAnswersWithPoints(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)
	checks := door1Checks(t)
	verifyAs(h, "1", previewerToken, []results.CheckResult{{ID: checks[0].ID, Passed: true}, {ID: checks[1].ID}})
	*h.clock = h.clock.Add(time.Minute)
	readOpened(t, openHint(h, "1", previewerToken, `{"hint": 1, "cost": 5}`))
	*h.clock = h.clock.Add(time.Minute)
	rec := verifyAs(h, "1", previewerToken, resultsFor(checks, true))
	var body verifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Score == nil || *body.Score != (doorScore{Points: 95, Hints: 1, Preview: true}) {
		t.Errorf("score %+v", body.Score)
	}
}

// Without the database for the points, the run is still saved and says so.
func TestVerifyWithoutPoints(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	h.results.progressDown = true
	rec := verifyAs(h, "1", playerToken, resultsFor(door1Checks(t), true))
	var body verifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK || !body.Passed || body.Score != nil {
		t.Errorf("status %d, %+v, %v", rec.Code, body, err)
	}
}
