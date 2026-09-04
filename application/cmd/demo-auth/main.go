// Command demo-auth creates ephemeral development keys and request-bound
// assertions for the disposable demo. It is not linked into the server binary.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fabianhjr/BondExchange/application/cmd/internal/demoauth"
)

const (
	demoIssuer   = demoauth.Issuer
	demoAudience = demoauth.Audience
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 2 && arguments[0] == "init" {
		return initialize(arguments[1])
	}
	if len(arguments) == 6 && arguments[0] == "token" {
		return issue(arguments[1], arguments[2], arguments[3], arguments[4], arguments[5])
	}
	return errors.New("usage: demo-auth init DIR | demo-auth token PRIVATE_JWK SUBJECT OPERATION IDEMPOTENCY_KEY_OR_DASH REQUEST_JSON")
}

func initialize(directory string) error {
	return demoauth.Initialize(directory)
}

func issue(privateKeyPath, subject, operation, idempotencyKey, requestJSON string) error {
	serialized, err := issueToken(privateKeyPath, subject, operation, idempotencyKey, requestJSON, time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Println(serialized)
	return nil
}

func issueToken(privateKeyPath, subject, operation, idempotencyKey, requestJSON string, now time.Time) (string, error) {
	return demoauth.IssueToken(privateKeyPath, subject, operation, idempotencyKey, requestJSON, now)
}
