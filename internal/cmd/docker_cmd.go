package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

// NewDockerCmd creates the docker subcommand for runtime management.
func NewDockerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docker",
		Short: "Docker runtime management",
	}

	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build the orchestra-agent Docker image",
		RunE: func(cmd *cobra.Command, args []string) error {
			dockerfilePath := "Dockerfile.agent"
			if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
				return fmt.Errorf("Dockerfile.agent not found in current directory — run this from the orchestra repo root")
			}

			build := exec.Command("docker", "build", "-t", "orchestra-agent:latest", "-f", dockerfilePath, ".")
			build.Stdout = os.Stdout
			build.Stderr = os.Stderr
			return build.Run()
		},
	}

	tokenCmd := &cobra.Command{
		Use:   "token",
		Short: "Extract CLAUDE_CODE_OAUTH_TOKEN from system keychain",
		Long: `Extract the OAuth access token from the Claude Code keychain credential
and print an export command for setting CLAUDE_CODE_OAUTH_TOKEN.

Usage:
  eval $(orchestra docker token)

On macOS, reads from the "Claude Code-credentials" keychain entry.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := extractOAuthToken()
			if err != nil {
				return err
			}
			fmt.Printf("export CLAUDE_CODE_OAUTH_TOKEN=%s\n", token)
			return nil
		},
	}

	cmd.AddCommand(buildCmd, tokenCmd)
	return cmd
}

// extractOAuthToken reads the Claude Code OAuth token from the system keychain.
func extractOAuthToken() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("keychain extraction only supported on macOS — set CLAUDE_CODE_OAUTH_TOKEN manually")
	}

	out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return "", fmt.Errorf("failed to read keychain: %w\nRun 'claude setup-token' first to create credentials", err)
	}

	// Parse the JSON credential blob
	var creds map[string]json.RawMessage
	if err := json.Unmarshal(out, &creds); err != nil {
		return "", fmt.Errorf("failed to parse keychain JSON: %w", err)
	}

	// Token lives at claudeAiOauth.accessToken
	oauthRaw, ok := creds["claudeAiOauth"]
	if !ok {
		return "", fmt.Errorf("no claudeAiOauth key in keychain — run 'claude setup-token' first")
	}

	var oauth struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(oauthRaw, &oauth); err != nil {
		return "", fmt.Errorf("failed to parse OAuth credentials: %w", err)
	}

	if oauth.AccessToken == "" {
		return "", fmt.Errorf("accessToken is empty — run 'claude setup-token' to generate one")
	}

	return oauth.AccessToken, nil
}
