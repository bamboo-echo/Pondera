package cloudsync

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"pondera/internal/proc"
)

// Git bundle staging entry names (inside workspace/ of a TaskBundle).
const (
	GitBundleName   = "git.bundle"
	HandoffPatch    = "handoff.patch"
	UntrackedPrefix = "untracked"
)

// InspectWorkspace reports how a workspace can be synchronised.
func InspectWorkspace(root string) (WorkspaceInfo, error) {
	root = filepath.Clean(root)
	st, err := os.Stat(root)
	if err != nil {
		return WorkspaceInfo{}, fmt.Errorf("cloudsync: stat workspace: %w", err)
	}
	if !st.IsDir() {
		return WorkspaceInfo{}, errors.New("cloudsync: workspace root is not a directory")
	}
	info := WorkspaceInfo{Root: root, Strategy: "file-list"}
	if _, err := exec.LookPath("git"); err != nil {
		return info, nil
	}
	inside, err := gitOutput(root, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return info, nil
	}
	info.IsGit = true
	info.Strategy = "git-bundle"
	if head, err := gitOutput(root, "rev-parse", "HEAD"); err == nil {
		info.HeadCommit = strings.TrimSpace(head)
	}
	status, err := gitOutput(root, "status", "--porcelain")
	if err == nil {
		// Any tracked modification (staged or not) counts as dirty; untracked
		// files are carried separately by the file-list stage.
		for _, line := range strings.Split(status, "\n") {
			if len(line) > 2 && !strings.HasPrefix(line, "??") {
				info.Dirty = true
				break
			}
		}
	}
	return info, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := proc.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// PrepareWorkspace stages a workspace for bundling and returns the directory
// that Build should use as WorkspaceDir, plus the resulting WorkspaceInfo.
//
// For a git repository the staged directory contains a self-contained bundle,
// a binary patch for tracked-but-uncommitted changes, and a copy of untracked
// files. The user's repository is never modified: we use `git bundle create`
// and `git diff`, never add/commit/stash.
// For a non-git directory the original root is returned unchanged and Build
// copies it with the file-list strategy (applying the exclude rules).
func PrepareWorkspace(root, stagingDir string, excludes []string) (string, WorkspaceInfo, error) {
	info, err := InspectWorkspace(root)
	if err != nil {
		return "", WorkspaceInfo{}, err
	}
	if !info.IsGit {
		return root, info, nil
	}
	if excludes == nil {
		excludes = DefaultExcludes
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return "", WorkspaceInfo{}, err
	}
	bundlePath := filepath.Join(stagingDir, GitBundleName)
	if _, err := gitOutput(root, "bundle", "create", bundlePath, "--all"); err != nil {
		return "", WorkspaceInfo{}, fmt.Errorf("cloudsync: create git bundle: %w", err)
	}
	info.BundlePath = GitBundleName

	if info.Dirty {
		patchPath := filepath.Join(stagingDir, HandoffPatch)
		diff, err := gitOutputBytes(root, "diff", "--binary", "HEAD")
		if err != nil {
			return "", WorkspaceInfo{}, fmt.Errorf("cloudsync: capture working tree diff: %w", err)
		}
		if err := os.WriteFile(patchPath, diff, 0o600); err != nil {
			return "", WorkspaceInfo{}, err
		}
	}

	untracked, err := gitOutput(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", WorkspaceInfo{}, err
	}
	for _, rel := range strings.Split(untracked, "\x00") {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		if ShouldExclude(UntrackedPrefix+"/"+filepath.ToSlash(rel), excludes) {
			continue
		}
		src := filepath.Join(root, filepath.FromSlash(rel))
		st, err := os.Lstat(src)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		dst := filepath.Join(stagingDir, UntrackedPrefix, filepath.FromSlash(rel))
		if err := copyFile(src, dst, st.Size()); err != nil {
			return "", WorkspaceInfo{}, fmt.Errorf("cloudsync: stage untracked %s: %w", rel, err)
		}
	}
	return stagingDir, info, nil
}

func gitOutputBytes(dir string, args ...string) ([]byte, error) {
	cmd := proc.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

func copyFile(src, dst string, size int64) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(in, size))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size {
		return errors.New("cloudsync: short copy")
	}
	return nil
}

// ApplyWorkspace materialises a workspace from an extracted bundle into dest.
// It is used after OpenBundle(...).Extract(staging): workspaceRoot is
// <staging>/workspace.
func ApplyWorkspace(workspaceRoot, dest string, info WorkspaceInfo) error {
	if strings.TrimSpace(dest) == "" {
		return errors.New("cloudsync: empty destination")
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	if !info.IsGit {
		return nil // non-git payload is already extracted by Build's file-list
	}
	bundle := filepath.Join(workspaceRoot, GitBundleName)
	if _, err := os.Stat(bundle); err != nil {
		return fmt.Errorf("cloudsync: missing git bundle: %w", err)
	}
	cmd := proc.Command("git", "clone", "--no-checkout", bundle, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cloudsync: clone from bundle: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if info.HandoffCommit != "" {
		if _, err := gitOutput(dest, "checkout", "--detach", info.HandoffCommit); err != nil {
			return err
		}
	} else if info.HeadCommit != "" {
		if _, err := gitOutput(dest, "checkout", "--detach", info.HeadCommit); err != nil {
			return err
		}
	}
	patch := filepath.Join(workspaceRoot, HandoffPatch)
	if _, err := os.Stat(patch); err == nil {
		if _, err := gitOutput(dest, "apply", "--binary", patch); err != nil {
			return fmt.Errorf("cloudsync: apply handoff patch: %w", err)
		}
	}
	untrackedRoot := filepath.Join(workspaceRoot, UntrackedPrefix)
	if st, err := os.Stat(untrackedRoot); err == nil && st.IsDir() {
		if err := copyTree(untrackedRoot, dest); err != nil {
			return err
		}
	}
	return nil
}

func copyTree(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(srcRoot, p)
		if err != nil {
			return err
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(p, filepath.Join(dstRoot, rel), st.Size())
	})
}
