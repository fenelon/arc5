package email

import (
	"context"
	"errors"

	"github.com/fenelon/arc5/internal/connector"
)

func (c *Connector) run(ctx context.Context, sink connector.MailSink, state connector.State, a account, o options) error {
	return errors.New("email: capture is not built yet")
}
