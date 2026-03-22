package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ResolveSecret resolves a secret value using the env:/file:/cmd: prefix convention.
// If the value has no recognized prefix, it is returned as-is (literal value).
//
// Supported prefixes:
//
//	env:VAR_NAME       - read from environment variable
//	file:/path/to/file - read from file (trailing newline stripped)
//	cmd:command args   - execute command, use stdout (trailing newline stripped)
func ResolveSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}

	prefix, rest, ok := splitPrefix(value)
	if !ok {
		// No recognized prefix -- treat as literal value.
		return value, nil
	}

	switch prefix {
	case "env":
		return resolveEnv(rest)
	case "file":
		return resolveFile(rest)
	case "cmd":
		return resolveCmd(rest)
	default:
		// Should not happen given splitPrefix logic, but be safe.
		return value, nil
	}
}

// splitPrefix checks for env:, file:, or cmd: prefixes.
// Returns the prefix name, the remainder, and true if a recognized prefix was found.
func splitPrefix(value string) (prefix, rest string, ok bool) {
	for _, p := range []string{"env:", "file:", "cmd:"} {
		if strings.HasPrefix(value, p) {
			return p[:len(p)-1], value[len(p):], true
		}
	}
	return "", "", false
}

// resolveEnv reads a secret from an environment variable.
func resolveEnv(varName string) (string, error) {
	if varName == "" {
		return "", fmt.Errorf("config.ResolveSecret: env: variable name is empty")
	}
	val, ok := os.LookupEnv(varName)
	if !ok {
		return "", fmt.Errorf("config.ResolveSecret: environment variable %q is not set", varName)
	}
	return val, nil
}

// resolveFile reads a secret from a file. Trailing newlines are stripped.
func resolveFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("config.ResolveSecret: file: path is empty")
	}
	path = ExpandPath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("config.ResolveSecret: file:%s: %w", path, err)
	}
	return strings.TrimRight(string(data), "\n\r"), nil
}

// resolveCmd executes a command and returns its stdout. Trailing newlines are stripped.
// The command is run via /bin/sh -c on Unix.
func resolveCmd(cmdStr string) (string, error) {
	if cmdStr == "" {
		return "", fmt.Errorf("config.ResolveSecret: cmd: command is empty")
	}
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("config.ResolveSecret: cmd:%s: %w", cmdStr, err)
	}
	return strings.TrimRight(string(out), "\n\r"), nil
}

// ResolveDestSecrets resolves all secret references in a destination config.
// This modifies the DestConfig in place, replacing env:/file:/cmd: values
// with their resolved plaintext. Call this before using credentials.
//
// Environment variable overrides take precedence (DOOMSDAY_<DEST>_KEY_ID, etc.).
func ResolveDestSecrets(dest *DestConfig, destName string) error {
	// Check env var overrides first (highest precedence)
	upper := strings.ToUpper(strings.ReplaceAll(destName, "-", "_"))
	if v := os.Getenv("DOOMSDAY_" + upper + "_KEY_ID"); v != "" {
		dest.KeyID = v
	}
	if v := os.Getenv("DOOMSDAY_" + upper + "_SECRET_KEY"); v != "" {
		dest.SecretKey = v
	}
	if v := os.Getenv("DOOMSDAY_" + upper + "_PASSWORD"); v != "" {
		dest.Password = v
	}

	// Then resolve any env:/file:/cmd: prefixes in remaining values
	fields := []*string{
		&dest.KeyID,
		&dest.SecretKey,
		&dest.Password,
	}
	for _, f := range fields {
		if *f == "" {
			continue
		}
		resolved, err := ResolveSecret(*f)
		if err != nil {
			return fmt.Errorf("config.ResolveDestSecrets: %w", err)
		}
		*f = resolved
	}
	return nil
}

// ResolveKey resolves the encryption key using the env:/file:/cmd: convention.
// Also checks DOOMSDAY_KEY environment variable as an override.
func ResolveKey(cfg *Config) (string, error) {
	// Environment override
	if v := os.Getenv("DOOMSDAY_KEY"); v != "" {
		return v, nil
	}
	if cfg.Key == "" {
		return "", fmt.Errorf("config.ResolveKey: key is not set")
	}
	return ResolveSecret(cfg.Key)
}
