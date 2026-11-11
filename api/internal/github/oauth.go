package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth is the website's half of the GitHub OAuth app: sending people to GitHub to log in,
// and swapping the code GitHub sends back for a token. The CLI uses the same app with the
// device flow, which needs no secret.
type OAuth struct {
	ClientID     string
	ClientSecret string
	// BaseURL is where people log in, https://github.com. Tests point it elsewhere.
	BaseURL string
	Client  *http.Client
}

// ErrBadCode means GitHub doesn't accept the code: used before, expired or made up.
var ErrBadCode = errors.New("GitHub doesn't accept this code")

// NewOAuth logs in on github.com.
func NewOAuth(clientID, clientSecret string) *OAuth {
	return &OAuth{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		BaseURL:      "https://github.com",
		Client:       &http.Client{Timeout: 10 * time.Second},
	}
}

// AuthorizeURL is where the browser goes to log in. No scope: the token can only read
// public profile data, like the CLI's.
func (o *OAuth) AuthorizeURL(redirectURI, state string) string {
	q := url.Values{
		"client_id":    {o.ClientID},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	return o.BaseURL + "/login/oauth/authorize?" + q.Encode()
}

// Exchange swaps the code from GitHub's redirect for a token.
func (o *OAuth) Exchange(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{
		"client_id":     {o.ClientID},
		"client_secret": {o.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := o.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("swapping the code with GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("swapping the code with GitHub: %s", resp.Status)
	}

	// GitHub answers 200 for a bad code too, with an error in the body.
	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("reading GitHub's answer: %w", err)
	}
	switch {
	case body.Error == "bad_verification_code":
		return "", ErrBadCode
	case body.Error != "":
		return "", fmt.Errorf("swapping the code with GitHub: %s", body.Error)
	case body.AccessToken == "":
		return "", errors.New("GitHub answered without a token")
	}
	return body.AccessToken, nil
}
