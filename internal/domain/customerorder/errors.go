package customerorder

import "fmt"

func wrapLine(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrLineWithoutPromise, fmt.Sprintf(format, args...))
}

func wrapDestination(groupNo int, prefix string) error {
	return fmt.Errorf("%w: group %d promise destination %q does not match the ship-to", ErrPromiseDestinationMismatch, groupNo, prefix)
}

func wrapUnknownGroup(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnknownGroup, fmt.Sprintf(format, args...))
}
