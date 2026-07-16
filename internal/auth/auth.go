package auth

import (
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// Method describes how to authenticate an SSH session.
type Method struct {
	KeyPath  string
	Password string
}

func (m Method) Signers() ([]ssh.Signer, error) {
	if m.KeyPath == "" {
		return nil, nil
	}

	key, err := os.ReadFile(m.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", m.KeyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		passphrase := m.Password
		if passphrase == "" {
			pw, promptErr := PromptPassword("key passphrase")
			if promptErr != nil {
				return nil, fmt.Errorf("parse key %s: %w", m.KeyPath, err)
			}
			passphrase = pw
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("parse key %s: %w", m.KeyPath, err)
		}
	}

	return []ssh.Signer{signer}, nil
}

func PromptPassword(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return string(b), nil
	}

	var pw string
	if _, err := fmt.Fscanln(os.Stdin, &pw); err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return pw, nil
}
