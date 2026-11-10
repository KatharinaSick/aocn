// Package api talks to the aocn API, which decides whether a door is open. The CLI never
// decides that itself.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/katharinasick/aocn/cli/internal/auth"
	"github.com/katharinasick/aocn/cli/internal/check"
)

// defaultURL is the API behind the website's /api/* proxy. AOCN_API_URL replaces it, e.g.
// with http://localhost:8080 for a local API.
const defaultURL = "https://aocn.dev/api"

// BaseURL is where the API is, without a trailing slash.
func BaseURL() string {
	if u := os.Getenv("AOCN_API_URL"); u != "" {
		return strings.TrimSuffix(u, "/")
	}
	return defaultURL
}

// host names the API in errors: aocn.dev normally, localhost:8080 when AOCN_API_URL points
// at a local API, so an error says which one couldn't be reached.
func host() string {
	if u, err := url.Parse(BaseURL()); err == nil && u.Host != "" {
		return u.Host
	}
	return BaseURL()
}

// ClosedError means the door exists but isn't open yet.
type ClosedError struct {
	Door    int
	OpensAt time.Time
}

// stationTime is CET, the same moment for every player, never their own time zone.
var stationTime = time.FixedZone("CET", 60*60)

func (e *ClosedError) Error() string {
	return fmt.Sprintf("door %d opens %s", e.Door, e.OpensAt.In(stationTime).Format("Jan 2, 15:04 MST"))
}

// ErrNoDoor means there is no door with that number.
var ErrNoDoor = errors.New("there is no such door")

// client gives up on a download that stalls. Bundles are small.
var client = &http.Client{Timeout: 2 * time.Minute}

// do sends a request to the API, with the player's token once they signed in. The API only
// looks at it when it needs to: for a previewer's early door, and to save a result.
func do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, BaseURL()+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// A token that can't be read is sent as no token: an open door doesn't need one, and
	// aocn verify reports the problem itself before it saves a result.
	if token, err := auth.Token(); err == nil && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach %s: %w", host(), err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, responseError(resp)
	}
	return resp, nil
}

// Preview says a door was handed out before it opens, because the player is a previewer.
// Zero for an open door.
type Preview struct {
	OpensAt time.Time
}

// DownloadBundle writes an open door's bundle to w. It returns a *ClosedError before the
// door opens and ErrNoDoor for a door that doesn't exist.
func DownloadBundle(ctx context.Context, door int, w io.Writer) (Preview, error) {
	resp, err := do(ctx, http.MethodGet, fmt.Sprintf("/doors/%d/bundle", door), nil)
	if err != nil {
		return Preview{}, err
	}
	defer resp.Body.Close()

	var preview Preview
	if v := resp.Header.Get("Aocn-Preview"); v != "" {
		preview.OpensAt, _ = time.Parse(time.RFC3339, v)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return Preview{}, fmt.Errorf("downloading door %d failed: %w", door, err)
	}
	return preview, nil
}

// Checks returns the door's checks. They only ever come from here, so nothing in the
// player's folder decides whether a door passes.
func Checks(ctx context.Context, door int) ([]check.Check, error) {
	resp, err := do(ctx, http.MethodGet, fmt.Sprintf("/doors/%d/checks", door), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Checks []check.Check `json:"checks"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("reading door %d's checks: %w", door, err)
	}
	return body.Checks, nil
}

// Saved is what the API kept of a run.
type Saved struct {
	Passed bool   `json:"passed"`
	Login  string `json:"login"`
}

// SendResults saves a run under the signed-in player's GitHub account.
func SendResults(ctx context.Context, door int, outcomes []check.Outcome) (Saved, error) {
	body, err := json.Marshal(struct {
		Results []check.Outcome `json:"results"`
	}{outcomes})
	if err != nil {
		return Saved{}, err
	}
	resp, err := do(ctx, http.MethodPost, fmt.Sprintf("/doors/%d/verify", door), bytes.NewReader(body))
	if err != nil {
		return Saved{}, err
	}
	defer resp.Body.Close()
	var saved Saved
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&saved); err != nil {
		return Saved{}, fmt.Errorf("reading %s's answer: %w", host(), err)
	}
	return saved, nil
}

// responseError turns an answer other than 200 into an error a player can act on.
func responseError(resp *http.Response) error {
	var body struct {
		Door    int       `json:"door"`
		OpensAt time.Time `json:"opensAt"`
		Error   string    `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)

	switch {
	case resp.StatusCode == http.StatusNotFound && !body.OpensAt.IsZero():
		return &ClosedError{Door: body.Door, OpensAt: body.OpensAt}
	case resp.StatusCode == http.StatusNotFound:
		return ErrNoDoor
	case body.Error != "":
		return fmt.Errorf("%s: %s", host(), body.Error)
	default:
		return fmt.Errorf("%s answered %s, try again in a few minutes", host(), resp.Status)
	}
}
