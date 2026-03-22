package notify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// CommandNotifier sends notifications by executing a shell command.
// The command is run via "sh -c" with event details available as
// environment variables: DOOMSDAY_STATUS, DOOMSDAY_MESSAGE, DOOMSDAY_CONFIG.
type CommandNotifier struct {
	Command string
}

// NewCommandNotifier creates a notifier that executes the given shell command.
func NewCommandNotifier(command string) *CommandNotifier {
	return &CommandNotifier{Command: command}
}

// Send executes the configured command with event details in the environment.
func (n *CommandNotifier) Send(ctx context.Context, event Event) error {
	if n.Command == "" {
		return fmt.Errorf("notify.CommandNotifier.Send: command is empty")
	}

	// SECURITY: Do NOT expand template variables into the command string —
	// this would allow shell injection via attacker-controlled event fields
	// (e.g., config names, error messages). Event data is passed safely via
	// environment variables instead. Users should use $DOOMSDAY_STATUS,
	// $DOOMSDAY_MESSAGE, $DOOMSDAY_CONFIG in their commands.
	cmd := exec.CommandContext(ctx, "sh", "-c", n.Command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set event details as environment variables for the child process.
	cmd.Env = append(os.Environ(),
		"DOOMSDAY_STATUS="+event.Status,
		"DOOMSDAY_MESSAGE="+event.Message,
		"DOOMSDAY_CONFIG="+event.Config,
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("notify.CommandNotifier.Send: %w", err)
	}
	return nil
}

