package credentials

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type Keychain struct {
	command string
}

func NewKeychain() *Keychain {
	return &Keychain{command: "security"}
}

func (k *Keychain) Resolve(ctx context.Context, reference string) (string, error) {
	if strings.HasPrefix(reference, "env://") {
		name := strings.TrimPrefix(reference, "env://")
		value, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("environment secret %s is not set", name)
		}
		return value, nil
	}
	service, account, err := parseReference(reference)
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("OS Keychain adapter is only available on macOS")
	}
	output, err := exec.CommandContext(
		ctx, k.command, "find-generic-password",
		"-s", service, "-a", account, "-w",
	).Output()
	if err != nil {
		return "", fmt.Errorf("load Keychain item %s/%s: %w", service, account, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (k *Keychain) Save(
	ctx context.Context,
	reference, secret string,
) error {
	service, account, err := parseReference(reference)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("OS Keychain adapter is only available on macOS")
	}
	if secret == "" {
		return fmt.Errorf("cannot save an empty secret")
	}
	output, err := exec.CommandContext(
		ctx, k.command, "add-generic-password",
		"-U", "-s", service, "-a", account, "-w", secret,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("save Keychain item %s/%s: %w: %s", service, account, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func parseReference(reference string) (string, string, error) {
	const prefix = "keychain://"
	if !strings.HasPrefix(reference, prefix) {
		return "", "", fmt.Errorf("unsupported secret reference %q", reference)
	}
	path := strings.Trim(strings.TrimPrefix(reference, prefix), "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("Keychain reference must be keychain://service/account")
	}
	return parts[0], parts[1], nil
}
