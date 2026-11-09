// Package api talks to the aocn API, which decides whether a door is open. The CLI never
// decides that itself.
package api

import (
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

// DownloadBundle writes an open door's bundle to w. It returns a *ClosedError before the
// door opens and ErrNoDoor for a door that doesn't exist.
func DownloadBundle(ctx context.Context, door int, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/doors/%d/bundle", BaseURL(), door), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach %s: %w", host(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return responseError(resp)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("downloading door %d failed: %w", door, err)
	}
	return nil
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
