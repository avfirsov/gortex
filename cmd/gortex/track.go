package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/daemon"
)

var trackCmd = &cobra.Command{
	Use:   "track <path>",
	Short: "Add a repository to the tracked workspace",
	Long:  "Resolves the path to absolute, validates it exists, and adds it to the global config.",
	Args:  cobra.ExactArgs(1),
	RunE:  runTrack,
}

var untrackCmd = &cobra.Command{
	Use:   "untrack <path>",
	Short: "Remove a repository from the tracked workspace",
	Long:  "Resolves the path and removes the matching entry from the global config.",
	Args:  cobra.ExactArgs(1),
	RunE:  runUntrack,
}

func init() {
	rootCmd.AddCommand(trackCmd)
	rootCmd.AddCommand(untrackCmd)
}

func runTrack(_ *cobra.Command, args []string) error {
	rawPath := args[0]

	// Resolve to absolute path.
	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return fmt.Errorf("resolving path %s: %w", rawPath, err)
	}

	// Validate path exists and is a directory.
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("path does not exist: %s", absPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", absPath)
	}

	// Daemon-first: if a daemon is running, it's the source of truth for
	// tracked repos and it'll index immediately. Falls through to the
	// config-only behavior below when no daemon is listening.
	if daemon.IsRunning() {
		c, err := daemon.Dial(daemon.Handshake{Mode: daemon.ModeControl, ClientName: "cli"})
		if err == nil {
			defer func() { _ = c.Close() }()
			resp, ctlErr := c.Control(daemon.ControlTrack, daemon.TrackParams{Path: absPath})
			if ctlErr != nil {
				return ctlErr
			}
			if !resp.OK {
				return fmt.Errorf("track rejected: %s %s", resp.ErrorCode, resp.ErrorMsg)
			}
			fmt.Fprintf(os.Stderr, "[gortex] tracked %s (via daemon)\n", absPath)
			return nil
		}
	}

	// Standalone fallback: update the config file directly. The daemon
	// (if later started) will pick this up on its next startup.
	gc, err := config.LoadGlobal()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}

	for _, existing := range gc.Repos {
		existingAbs, _ := filepath.Abs(existing.Path)
		if existingAbs == absPath {
			fmt.Fprintf(os.Stderr, "[gortex] already tracked: %s\n", absPath)
			return nil
		}
	}

	entry := config.RepoEntry{Path: absPath}
	if err := gc.AddRepo(entry); err != nil {
		return err
	}
	if err := gc.Save(); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[gortex] tracked %s (config only — start daemon to index)\n", absPath)
	return nil
}

func runUntrack(_ *cobra.Command, args []string) error {
	rawPath := args[0]

	// Argument can be either a path or a repo prefix; the daemon accepts
	// both. Resolve to absolute only when it looks like a path (starts
	// with / or . or has a path separator); otherwise treat as a prefix.
	target := rawPath
	if filepath.IsAbs(rawPath) || rawPath == "." || rawPath == ".." {
		abs, err := filepath.Abs(rawPath)
		if err != nil {
			return fmt.Errorf("resolving path %s: %w", rawPath, err)
		}
		target = abs
	}

	if daemon.IsRunning() {
		c, err := daemon.Dial(daemon.Handshake{Mode: daemon.ModeControl, ClientName: "cli"})
		if err == nil {
			defer func() { _ = c.Close() }()
			resp, ctlErr := c.Control(daemon.ControlUntrack, daemon.UntrackParams{PathOrPrefix: target})
			if ctlErr != nil {
				return ctlErr
			}
			if !resp.OK {
				return fmt.Errorf("untrack rejected: %s %s", resp.ErrorCode, resp.ErrorMsg)
			}
			fmt.Fprintf(os.Stderr, "[gortex] untracked %s (via daemon)\n", target)
			return nil
		}
	}

	// Standalone fallback.
	gc, err := config.LoadGlobal()
	if err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}
	if err := gc.RemoveRepo(target); err != nil {
		return err
	}
	if err := gc.Save(); err != nil {
		return fmt.Errorf("saving global config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[gortex] untracked %s (config only)\n", target)
	return nil
}
