package auth

import (
	"errors"
	"testing"
	"time"
)

func TestNextStep(t *testing.T) {
	interval := 5 * time.Second

	if token, _, err := nextStep(tokenResponse{AccessToken: "gho_x"}, interval); token != "gho_x" || err != nil {
		t.Errorf("token: got %q, %v", token, err)
	}

	waits := map[string]struct {
		resp tokenResponse
		want time.Duration
	}{
		"pending":            {tokenResponse{Error: "authorization_pending"}, 5 * time.Second},
		"slow down":          {tokenResponse{Error: "slow_down"}, 10 * time.Second},
		"slow down, told by": {tokenResponse{Error: "slow_down", Interval: 15}, 15 * time.Second},
	}
	for name, tt := range waits {
		token, next, err := nextStep(tt.resp, interval)
		if token != "" || err != nil || next != tt.want {
			t.Errorf("%s: got %q, %v, %v, want to wait %v", name, token, next, err, tt.want)
		}
	}

	for name, resp := range map[string]tokenResponse{
		"expired":         {Error: "expired_token"},
		"denied":          {Error: "access_denied"},
		"disabled":        {Error: "device_flow_disabled", Description: "Device Flow must be explicitly enabled for this App"},
		"unknown":         {Error: "something_new"},
		"no token at all": {},
	} {
		if token, _, err := nextStep(resp, interval); token != "" || err == nil {
			t.Errorf("%s: got %q, %v, want an error", name, token, err)
		}
	}

	if _, _, err := nextStep(tokenResponse{Error: "expired_token"}, interval); !errors.Is(err, errCodeExpired) {
		t.Errorf("expired: got %v", err)
	}
}

func TestPollInterval(t *testing.T) {
	for given, want := range map[int]time.Duration{0: 5 * time.Second, 1: 5 * time.Second, 5: 5 * time.Second, 8: 8 * time.Second} {
		if got := pollInterval(DeviceCode{Interval: given}); got != want {
			t.Errorf("interval %d: got %v, want %v", given, got, want)
		}
	}
}
