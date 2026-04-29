package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/happydev123/backendlab/internal/auth"
)

const hashPasswordUsage = "hash-password < password.txt"

// hashPasswordCommand reads the operator password from the first line of
// stdin and prints its bcrypt hash for BACKENDLAB_ADMIN_PASSWORD_HASH.
// The password is never accepted as an argument, where it would end up in
// shell history and process listings.
func hashPasswordCommand(_ context.Context, app *App, args []string) int {
	fs := newFlagSet("hash-password", app.Stderr, hashPasswordUsage)
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	if len(pos) != 0 {
		return app.fail(usagef("hash-password reads the password from stdin, not from arguments"))
	}
	if app.Stdin == nil {
		return app.fail(usagef("no standard input available"))
	}
	fmt.Fprintln(app.Stderr, "reading password from the first line of stdin")
	sc := bufio.NewScanner(app.Stdin)
	sc.Buffer(make([]byte, 0, 256), 1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return app.fail(fmt.Errorf("read password: %w", err))
		}
		return app.fail(usagef("no password given on stdin"))
	}
	pw := strings.TrimRight(sc.Text(), "\r")
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return app.fail(err)
	}
	fmt.Fprintln(app.Stdout, hash)
	return ExitOK
}
