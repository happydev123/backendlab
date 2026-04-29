package cli

import (
	"context"
	"strings"
	"testing"
)

func TestHashPassword(t *testing.T) {
	app, stdout, _ := testApp(nil)
	app.Stdin = strings.NewReader("correct horse battery staple\n")
	if code := app.Run(context.Background(), []string{"hash-password"}); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.HasPrefix(stdout.String(), "$2") {
		t.Fatalf("stdout = %q", stdout)
	}
	app, _, _ = testApp(nil)
	app.Stdin = strings.NewReader("short\n")
	if code := app.Run(context.Background(), []string{"hash-password"}); code != ExitError {
		t.Fatalf("short password exit = %d", code)
	}
	app, _, _ = testApp(nil)
	app.Stdin = strings.NewReader("x\n")
	if code := app.Run(context.Background(), []string{"hash-password", "secret-in-args"}); code != ExitError {
		t.Fatalf("argument password exit = %d", code)
	}
}
