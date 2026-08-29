package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// cmdRelease tags the repo and publishes the bundle as a GitHub release asset
// using the gh CLI. Key/auth comes from the environment (gh must be authed).
func cmdRelease(args []string) error {
	if len(args) < 2 {
		return errors.New("release requires <slug> <version>")
	}
	slug, version := args[0], args[1]
	tag := flagValue(args, "--tag")
	if tag == "" {
		tag = slug + "-v" + version
	}
	zipFile := fmt.Sprintf("%s-%s.zip", slug, version)
	if _, err := os.Stat(zipFile); err != nil {
		return fmt.Errorf("bundle %s not found (run `flamegate-publisher bundle %s %s` first)", zipFile, slug, version)
	}

	// 1. Ensure a local tag exists.
	if err := ensureTag(tag); err != nil {
		return err
	}
	if err := runCmd("git", "push", "origin", tag); err != nil {
		return err
	}

	// 2. Create/verify the release.
	r := repo()
	if err := runCmd("gh", "release", "view", tag, "--repo", r); err != nil {
		// release does not exist yet — create it.
		return runCmd("gh", "release", "create", tag, zipFile, "--repo", r, "--generate-notes")
	}
	// Release exists — upload the asset.
	return runCmd("gh", "release", "upload", tag, zipFile, "--repo", r, "--clobber")
}

// ensureTag creates a tag at HEAD if it does not already exist.
func ensureTag(tag string) error {
	if err := runCmd("git", "rev-parse", tag); err == nil {
		return nil
	}
	return runCmd("git", "tag", tag)
}

// repo returns owner/repo from the git remote (best-effort). Supports both
// https://github.com/owner/repo and git@github.com:owner/repo forms.
func repo() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	u := strings.TrimSpace(string(out))
	u = strings.TrimSuffix(u, ".git")
	u = strings.TrimPrefix(u, "https://github.com/")
	u = strings.TrimPrefix(u, "git@github.com:")
	return strings.TrimLeft(u, "/")
}