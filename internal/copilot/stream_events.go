package copilot

import "context"

// StreamEvents returns a channel of StreamEvent for SSE streaming.
func (c *Client) StreamEvents(ctx context.Context, input CompletionInput) (<-chan StreamEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	session, sourceEvents, err := c.startPromptEvents(ctx, input)
	if err != nil {
		return nil, err
	}

	events := make(chan StreamEvent, 128)
	go c.forwardStreamEvents(ctx, session, sourceEvents, events)

	return events, nil
}

func (c *Client) forwardStreamEvents(
	ctx context.Context,
	session *SessionState,
	sourceEvents <-chan StreamEvent,
	events chan<- StreamEvent,
) {
	defer c.releaseSession(session)
	defer close(events)

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sourceEvents:
			if !ok {
				return
			}

			select {
			case events <- evt:
			case <-ctx.Done():
				return
			}

			switch evt.Type {
			case EventDone, EventError:
				return
			}
		}
	}
}
