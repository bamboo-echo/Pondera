// Package writer is the only path that may persist assistant changes. It
// implements the locked, backed-up, atomically-written transaction described in
// docs/ASSISTANT_DESIGN.zh-CN.md.
package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"pondera/internal/assist/patch"
	"pondera/internal/config"
	"pondera/internal/fileutil"
)

// ErrStale reports a plan whose base revision no longer matches the file.
var ErrStale = errors.New("stale_plan")

// Writer persists assistant plans. ConfigPath and BackupRoot default to the
// user config path and %AppData%/pondera/assistant/backups.
type Writer struct {
	ConfigPath string
	BackupRoot string
	// WriteFile is the atomic write seam. Production uses fileutil.AtomicWriteFile.
	WriteFile func(path string, data []byte, perm os.FileMode) error
	// SaveConfig is the config render/save seam. Production uses cfg.SaveTo.
	SaveConfig func(cfg *config.Config, path string) error
	Now        func() time.Time
}

// New returns a writer bound to the user configuration.
func New() *Writer {
	return &Writer{}
}

func (w *Writer) configPath() string {
	if strings.TrimSpace(w.ConfigPath) != "" {
		return w.ConfigPath
	}
	return config.UserConfigPath()
}

func (w *Writer) backupRoot() string {
	if strings.TrimSpace(w.BackupRoot) != "" {
		return w.BackupRoot
	}
	home := config.PonderaHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "assistant", "backups")
}

func (w *Writer) writeFile(path string, data []byte, perm os.FileMode) error {
	if w.WriteFile != nil {
		return w.WriteFile(path, data, perm)
	}
	return fileutil.AtomicWriteFile(path, data, perm)
}

func (w *Writer) saveConfig(cfg *config.Config, path string) error {
	if w.SaveConfig != nil {
		return w.SaveConfig(cfg, path)
	}
	return cfg.SaveTo(path)
}

func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// CurrentBaseHash returns the content hash of the current user config (or the
// hash of an absent file) plus the credential-store revision.
func (w *Writer) CurrentBaseHash() (string, string, error) {
	path := w.configPath()
	if path == "" {
		return "", "", fmt.Errorf("config path unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", "", err
	}
	return Hash(raw), config.CredentialStoreRevision(), nil
}

// Hash returns "sha256:<hex>" for raw bytes.
func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// BackupRef identifies one restorable snapshot.
type BackupRef struct {
	ID         string `json:"id"`
	Dir        string `json:"dir"`
	Hash       string `json:"hash"`
	CreatedAt  string `json:"created_at"`
	ConfigPath string `json:"config_path"`
}

// ApplyResult reports what the write committed.
type ApplyResult struct {
	Backup     BackupRef `json:"backup"`
	ConfigPath string    `json:"config_path"`
	Credential string    `json:"credential"`
	AppliedOps int       `json:"applied_ops"`
}

// Apply runs the full transaction:
//
//	backup → lock → strict re-read → revision compare → apply → SaveTo →
//	credential write → reload + assert → rollback on failure
func (w *Writer) Apply(ctx context.Context, plan *patch.Plan) (ApplyResult, error) {
	path := w.configPath()
	if path == "" {
		return ApplyResult{}, fmt.Errorf("config path unavailable")
	}
	if plan == nil || len(plan.Ops) == 0 {
		return ApplyResult{}, fmt.Errorf("empty plan")
	}
	if err := ctx.Err(); err != nil {
		return ApplyResult{}, err
	}

	unlock, err := config.LockConfigFilesEdits(path)
	if err != nil {
		return ApplyResult{}, err
	}
	defer unlock()

	if err := ctx.Err(); err != nil {
		return ApplyResult{}, err
	}
	cfg, err := config.LoadForEditWithoutCredentialsReadOnlyStrict(path)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("strict read failed; refuse to write: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return ApplyResult{}, err
	}
	if got := Hash(raw); got != plan.BaseHash {
		return ApplyResult{}, fmt.Errorf("%w: 配置在预览后已变化（预期 %s，实际 %s），请重新预览", ErrStale, plan.BaseHash, got)
	}
	if plan.CredentialRevision != "" && config.CredentialStoreRevision() != plan.CredentialRevision {
		return ApplyResult{}, fmt.Errorf("%w: 凭据库在预览后已变化，请重新预览", ErrStale)
	}

	ops, err := patch.Revalidate(plan.Ops, cfg)
	if err != nil {
		return ApplyResult{}, err
	}
	ref, err := w.backup(raw, plan)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("backup failed; refuse to write: %w", err)
	}
	rollback := func(cause error) (ApplyResult, error) {
		if restoreErr := w.restoreLocked(ref); restoreErr != nil {
			return ApplyResult{}, fmt.Errorf("%v; automatic rollback failed: %v (backup %s)", cause, restoreErr, ref.Dir)
		}
		return ApplyResult{}, cause
	}

	if err := patch.Apply(cfg, ops); err != nil {
		return rollback(err)
	}
	if err := w.saveConfig(cfg, path); err != nil {
		return rollback(fmt.Errorf("save config: %w", err))
	}

	credTarget := ""
	for _, secret := range patch.Secrets(ops) {
		key := secret.APIKeyEnv
		value, _ := secret.Value.(string)
		target, applied, err := config.SetCredentialIfRevision(key, value, plan.CredentialRevision)
		if err != nil {
			return rollback(fmt.Errorf("write credential %s: %w", key, err))
		}
		if !applied {
			return rollback(fmt.Errorf("%w: 凭据库在写入期间被其他进程修改", ErrStale))
		}
		credTarget = target
	}

	reloaded, err := config.LoadForEditWithoutCredentialsReadOnlyStrict(path)
	if err != nil {
		return rollback(fmt.Errorf("post-write reload failed: %w", err))
	}
	if err := patch.AssertApplied(reloaded, ops); err != nil {
		return rollback(err)
	}

	w.prune()
	return ApplyResult{Backup: ref, ConfigPath: path, Credential: credTarget, AppliedOps: len(ops)}, nil
}

// backup writes the raw pre-change bytes plus a redacted manifest.
func (w *Writer) backup(raw []byte, plan *patch.Plan) (BackupRef, error) {
	root := w.backupRoot()
	if root == "" {
		return BackupRef{}, fmt.Errorf("assistant backup root unavailable")
	}
	id := w.now().UTC().Format("20060102-150405") + "-" + plan.ID
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BackupRef{}, err
	}
	ref := BackupRef{ID: id, Dir: dir, Hash: Hash(raw), CreatedAt: w.now().UTC().Format(time.RFC3339Nano), ConfigPath: w.configPath()}
	if err := w.writeFile(filepath.Join(dir, "config.toml"), raw, 0o600); err != nil {
		return BackupRef{}, err
	}
	manifest, err := json.MarshalIndent(redactedManifest(ref, plan), "", "  ")
	if err != nil {
		return BackupRef{}, err
	}
	if err := w.writeFile(filepath.Join(dir, "manifest.json"), append(manifest, '\n'), 0o600); err != nil {
		return BackupRef{}, err
	}
	return ref, nil
}

type manifest struct {
	BackupRef
	PlanID string             `json:"plan_id"`
	Intent string             `json:"intent"`
	Ops    []patch.ResolvedOp `json:"ops"`
}

func redactedManifest(ref BackupRef, plan *patch.Plan) manifest {
	ops := make([]patch.ResolvedOp, 0, len(plan.Ops))
	for _, op := range plan.Ops {
		copied := op
		if op.Sensitive {
			copied.Value = "已设置（脱敏）"
			copied.Before = "已设置/未设置（脱敏）"
		}
		ops = append(ops, copied)
	}
	return manifest{BackupRef: ref, PlanID: plan.ID, Intent: plan.Intent, Ops: ops}
}

// restoreLocked writes backup bytes back over the config. Caller holds the lock.
func (w *Writer) restoreLocked(ref BackupRef) error {
	data, err := os.ReadFile(filepath.Join(ref.Dir, "config.toml"))
	if err != nil {
		return err
	}
	if Hash(data) != ref.Hash {
		return fmt.Errorf("backup hash mismatch: backup is corrupt")
	}
	if err := w.writeFile(ref.ConfigPath, data, 0o600); err != nil {
		return err
	}
	// Read-back assertion: the restored file must parse and match the backup.
	if _, err := config.LoadForEditWithoutCredentialsReadOnlyStrict(ref.ConfigPath); err != nil {
		return fmt.Errorf("restored file does not parse: %w", err)
	}
	after, err := os.ReadFile(ref.ConfigPath)
	if err != nil {
		return err
	}
	if Hash(after) != ref.Hash {
		return fmt.Errorf("restored file hash differs from backup")
	}
	return nil
}

// Rollback restores one backup by id under the config lock.
func (w *Writer) Rollback(backupID string) (BackupRef, error) {
	refs, err := w.ListBackups()
	if err != nil {
		return BackupRef{}, err
	}
	for _, ref := range refs {
		if ref.ID != backupID {
			continue
		}
		unlock, err := config.LockConfigFilesEdits(w.configPath())
		if err != nil {
			return BackupRef{}, err
		}
		defer unlock()
		if err := w.restoreLocked(ref); err != nil {
			return BackupRef{}, err
		}
		return ref, nil
	}
	return BackupRef{}, fmt.Errorf("backup %q not found", backupID)
}

// ListBackups returns local backups newest first.
func (w *Writer) ListBackups() ([]BackupRef, error) {
	root := w.backupRoot()
	if root == "" {
		return nil, fmt.Errorf("assistant backup root unavailable")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	refs := make([]BackupRef, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dir := filepath.Join(root, entry.Name())
			raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
			if err != nil {
				continue
			}
			var m manifest
			if err := json.Unmarshal(raw, &m); err != nil {
				continue
			}
			m.Dir = dir
			refs = append(refs, m.BackupRef)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID > refs[j].ID })
	return refs, nil
}

// prune keeps the newest ten backups and removes anything older than 30 days.
func (w *Writer) prune() {
	refs, err := w.ListBackups()
	if err != nil {
		return
	}
	cutoff := w.now().Add(-30 * 24 * time.Hour)
	for i, ref := range refs {
		created, parseErr := time.Parse(time.RFC3339Nano, ref.CreatedAt)
		if i < 10 && (parseErr != nil || !created.Before(cutoff)) {
			continue
		}
		_ = os.RemoveAll(ref.Dir)
	}
}
