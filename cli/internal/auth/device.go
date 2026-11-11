package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// clientID is the aocn OAuth app's. It's public: the device flow needs no secret, which is
// why a CLI can use it.
const clientID = "Ov23lim4gHxoibK0E5Tv"

// The token asks for no scope, so it can only read public profile data. Nothing it could do
// is worth stealing.
const scope = ""

var client = &http.Client{Timeout: 30 * time.Second}

const (
	// githubURL is where the device flow happens.
	githubURL = "https://github.com"
)

// DeviceCode is what the player types in on github.com, and what aocn polls with.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// StartDeviceFlow asks GitHub for a code the player confirms in their browser.
func StartDeviceFlow(ctx context.Context) (DeviceCode, error) {
	var code DeviceCode
	err := post(ctx, githubURL+"/login/device/code", url.Values{"client_id": {clientID}, "scope": {scope}}, &code)
	if err != nil {
		return DeviceCode{}, fmt.Errorf("asking GitHub for a code: %w", err)
	}
	if code.DeviceCode == "" || code.UserCode == "" {
		return DeviceCode{}, errors.New("GitHub didn't send a code. Try again in a minute")
	}
	return code, nil
}

// tokenResponse is GitHub's answer while aocn waits for the player.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error"`
	Description string `json:"error_description"`
	Interval    int    `json:"interval"`
}

// minInterval is the shortest wait between two polls: the device flow's default. It keeps aocn
// from asking GitHub in a tight loop if an answer ever comes without an interval.
const minInterval = 5 * time.Second

// pollInterval is how long to wait before the first poll: what GitHub said, never less than
// minInterval.
func pollInterval(code DeviceCode) time.Duration {
	return max(time.Duration(code.Interval)*time.Second, minInterval)
}

// WaitForToken polls GitHub until the player confirmed the code, and returns the token.
func WaitForToken(ctx context.Context, code DeviceCode) (string, error) {
	interval := pollInterval(code)
	deadline := time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return "", errCodeExpired
		}

		var resp tokenResponse
		err := post(ctx, githubURL+"/login/oauth/access_token", url.Values{
			"client_id":   {clientID},
			"device_code": {code.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &resp)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			// A network hiccup while the player is in their browser: keep waiting.
			continue
		}
		token, next, err := nextStep(resp, interval)
		if token != "" || err != nil {
			return token, err
		}
		interval = next
	}
}

var errCodeExpired = errors.New("the code expired before it was confirmed. Run aocn login again")

// nextStep reads GitHub's answer: the token, or how long to wait before asking again, or why
// signing in failed.
func nextStep(resp tokenResponse, interval time.Duration) (token string, next time.Duration, err error) {
	switch resp.Error {
	case "":
		if resp.AccessToken == "" {
			return "", 0, errors.New("GitHub answered without a token. Run aocn login again")
		}
		return resp.AccessToken, 0, nil
	case "authorization_pending":
		return "", interval, nil
	case "slow_down":
		// GitHub says how long to wait from now on, at least 5 seconds more than before.
		if resp.Interval > 0 {
			return "", time.Duration(resp.Interval) * time.Second, nil
		}
		return "", interval + 5*time.Second, nil
	case "expired_token":
		return "", 0, errCodeExpired
	case "access_denied":
		return "", 0, errors.New("signing in was cancelled on GitHub")
	}
	if resp.Description != "" {
		return "", 0, fmt.Errorf("GitHub: %s", resp.Description)
	}
	return "", 0, fmt.Errorf("GitHub: %s", resp.Error)
}

// post sends a form to GitHub and reads its JSON answer into v.
func post(ctx context.Context, u string, form url.Values, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(v)
}
