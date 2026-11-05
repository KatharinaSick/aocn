package email

import (
	"context"

	"github.com/katharinasick/aocn/api/internal/app"
)

// LogClient stands in for Resend in dev mode without an API key: it logs the
// confirmation link instead of emailing it, so the whole signup can be clicked through
// without an account. It logs addresses, so it must never run in production; the
// config refuses dev mode on Cloud Run.
type LogClient struct {
	log app.Logger
}

func NewLogClient(log app.Logger) *LogClient {
	return &LogClient{log: log}
}

func (c *LogClient) SendSubscribeConfirmation(_ context.Context, to, confirmURL string) error {
	c.log.Info("Would send subscribe confirmation", "to", to, "confirm_url", confirmURL)
	return nil
}

func (c *LogClient) AddSubscriber(_ context.Context, address string) error {
	c.log.Info("Would add subscriber", "email", address)
	return nil
}
