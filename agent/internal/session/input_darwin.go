//go:build darwin

// macOS input relay using CGEvent APIs via CGEventCreateMouseEvent/CGEventCreateKeyboardEvent.
package session

import (
	"context"
	"log/slog"
)

type darwinInputRelayer struct {
	logger *slog.Logger
}

func newPlatformInputRelayer(cfg InputRelayerConfig) (InputRelayer, error) {
	return &darwinInputRelayer{logger: cfg.Logger}, nil
}

func (r *darwinInputRelayer) MouseEvent(ctx context.Context, eventType string, x, y, button, wheelDelta int) error {
	// Not implemented yet (CGEvent APIs) — report failure, never fake success.
	r.logger.Debug("mouse event (not implemented)", "type", eventType, "x", x, "y", y, "button", button, "wheel", wheelDelta)
	return errNotImplemented
}

func (r *darwinInputRelayer) KeyboardEvent(ctx context.Context, eventType string, codepoint uint32, modifiers uint32) error {
	r.logger.Debug("keyboard event (not implemented)", "type", eventType, "codepoint", codepoint, "modifiers", modifiers)
	return errNotImplemented
}

func (r *darwinInputRelayer) Close() error {
	return nil
}
