package git

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// GetRepoFromGitRemote gets the repository owner/name from the git remote URL.
func GetRepoFromGitRemote() (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("could not get git remote URL: %w", err)
	}

	url := strings.TrimSpace(string(out))
	re := regexp.MustCompile(`(?:github\.com[/:])((?:[^/]+)/(?:[^/]+))(?:\.git)?$`)
	matches := re.FindStringSubmatch(url)

	if len(matches) < 2 {
		return "", fmt.Errorf("could not parse repository name from URL: %s", url)
	}

	return matches[1], nil
}
