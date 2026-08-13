package skills

import (
	"context"
	"errors"
	"fmt"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/config"
)

// Everything else in this package is inference. opentree reads the directories
// an agent is documented to read and the settings it is documented to honour,
// and says what that adds up to. Inference goes stale quietly: an agent that
// renames a directory produces an empty list rather than an error, and an
// override opentree misreads produces a row that is confidently wrong.
//
// ACP carries one piece of ground truth. available_commands_update is the
// agent naming what it actually loaded, and every skill is a command there.
// Probe asks for it, so the list can be checked against the thing it describes
// instead of only against the documentation.

// Probe starts the agent, opens a session, and reports the commands it
// advertises, keyed by name.
//
// It costs a real session in the agent's own storage — commands are a session
// update, so there is no way to be told without starting one. An empty
// conversation is cheap and the agent's own session list is full of them; the
// alternative is not being able to check at all.
func Probe(ctx context.Context, agent config.PredefinedAgent, cwd string) (map[string]bool, error) {
	// Buffered and non-blocking below: Update runs on the read loop, which must
	// not be held up by whether anyone is listening yet.
	commands := make(chan []acp.Command, 4)

	client, err := acp.Spawn(ctx, agent.ResolveACPCommand(), agent.ACPArgs(cwd), cwd, acp.Handlers{
		Update: func(u acp.SessionUpdate) {
			if u.Type == acp.UpdateCommands {
				select {
				case commands <- u.Commands:
				default:
				}
			}
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(ctx, "opentree", ""); err != nil {
		return nil, err
	}
	if _, err := client.NewSession(ctx, cwd); err != nil {
		return nil, err
	}

	select {
	case list := <-commands:
		out := make(map[string]bool, len(list))
		for _, c := range list {
			out[c.Name] = true
		}
		return out, nil
	case <-client.Done():
		return nil, errors.New("the agent exited before naming its commands")
	case <-ctx.Done():
		// Distinguished from a crash on purpose: an agent that started fine and
		// said nothing is telling us it has no commands to advertise, which is
		// itself an answer worth reading — but not one worth guessing at.
		return nil, fmt.Errorf("%s did not send its command list: %w", agent.Name, ctx.Err())
	}
}
