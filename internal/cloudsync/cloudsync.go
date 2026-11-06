package cloudsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Planner coordinates one-shot, user-initiated handoffs. It never performs
// network IO: moving the bundle to the other host is an explicit step (scp,
// object storage, or the existing remote/SFTP transport). This keeps the
// "manual switch" contract literal — nothing happens by itself.
type Planner struct {
	Store *Store
	Home  string
}

// PrepareUploadOptions describes an upload the user explicitly requested.
type PrepareUploadOptions struct {
	SessionID    string
	WorkspaceDir string
	SessionDir   string
	Checkpoints  []string
	OutDir       string
	// CloudHostID / CloudSessionID are the identity the destination will
	// issue. They are recorded in the origin mapping once the destination
	// confirms the import, not here.
	CloudHostID    string
	CloudSessionID string
	ConfigDigest   ConfigDigest
	Excludes       []string
	// TaskIDs scopes the session log to the selected tasks. When nil the
	// selection's own task refs are used (the "session + task" opt-in).
	TaskIDs []string
	// SliceTasks disables task-level slicing when explicitly false.
	SliceTasks *bool
	// SessionRoot is the sessions-v4 root used to discover a session directory
	// when SessionDir is empty. Empty falls back to <home>/sessions-v4.
	SessionRoot string
	Ctx         context.Context
	// BoundarySequence is the durable session position at capture time.
	BoundarySequence  uint64
	StorageGeneration string
	HeadTurnID        string
	Now               func() time.Time
}

// UploadPlan is a prepared, not-yet-committed handoff.
type UploadPlan struct {
	HandoffID  string
	BundlePath string
	Manifest   *Manifest
	Selection  Selection
	StagingDir string
	TaskWindow *TaskWindow
}

// PrepareUpload validates the selection, stages the workspace and builds a
// sealed bundle. It does NOT change ownership: the local machine stays the
// writer until the destination confirms a successful import.
//
// Quiescing (waiting for or cancelling a running turn) is the caller's job;
// this function refuses to proceed if the caller has not attested it.
func (p *Planner) PrepareUpload(opts PrepareUploadOptions, quiesced bool) (*UploadPlan, error) {
	if p.Store == nil {
		return nil, errors.New("cloudsync: nil store")
	}
	if !quiesced {
		return nil, errors.New("cloudsync: session must be quiesced before upload (no running turn)")
	}
	sel, ok := p.Store.Get(opts.SessionID)
	if !ok {
		return nil, fmt.Errorf("cloudsync: session %q has no sync policy", opts.SessionID)
	}
	if !sel.IsPinned() {
		return nil, fmt.Errorf("cloudsync: session %q is local-only; pin it first", opts.SessionID)
	}
	if sel.Owner == OwnerCloud {
		return nil, fmt.Errorf("cloudsync: session %q is owned by the cloud; download it before uploading again", opts.SessionID)
	}
	if strings.TrimSpace(opts.CloudHostID) == "" || strings.TrimSpace(opts.CloudSessionID) == "" {
		return nil, errors.New("cloudsync: destination identity is required")
	}
	localHostID := p.Store.LocalHostID()
	if localHostID == "" {
		return nil, errors.New("cloudsync: local host id is not set")
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		return nil, errors.New("cloudsync: empty output directory")
	}
	if err := os.MkdirAll(opts.OutDir, 0o700); err != nil {
		return nil, err
	}
	handoffID, err := NewHandoffID()
	if err != nil {
		return nil, err
	}
	staging := filepath.Join(opts.OutDir, ".stage-"+handoffID[:12])
	defer func() { _ = os.RemoveAll(staging) }()
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, err
	}

	excludes := opts.Excludes
	if excludes == nil {
		excludes = DefaultExcludes
	}
	var workspaceDir string
	var winfo WorkspaceInfo
	if strings.TrimSpace(opts.WorkspaceDir) != "" {
		workspaceDir, winfo, err = PrepareWorkspace(opts.WorkspaceDir, filepath.Join(staging, "workspace-stage"), excludes)
		if err != nil {
			return nil, err
		}
		if !winfo.IsGit {
			workspaceDir = opts.WorkspaceDir
		}
	} else {
		winfo = WorkspaceInfo{Strategy: "none"}
	}

	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// Resolve the session directory automatically when the caller did not
	// supply one, then scope its durable log to the selected tasks.
	sessionDir := strings.TrimSpace(opts.SessionDir)
	if sessionDir == "" && strings.TrimSpace(p.Home) != "" {
		if found, ferr := DiscoverSessionDir(p.Home, opts.SessionID); ferr == nil {
			sessionDir = found
		}
	}
	var taskWindow *TaskWindow
	if sessionDir != "" {
		taskIDs := opts.TaskIDs
		if taskIDs == nil {
			for _, t := range sel.Tasks {
				if id := strings.TrimSpace(t.ID); id != "" {
					taskIDs = append(taskIDs, id)
				}
			}
		}
		sliceEnabled := opts.SliceTasks == nil || *opts.SliceTasks
		if sliceEnabled {
			tw, serr := PrepareTaskSlice(ctx, sessionDir, filepath.Join(staging, "session-slice"), taskIDs)
			if serr != nil {
				return nil, serr
			}
			taskWindow = tw
			sessionDir = filepath.Join(staging, "session-slice")
		} else {
			taskWindow = &TaskWindow{TaskIDs: taskIDs, Reason: "slicing-disabled-whole-session"}
		}
	}

	bundlePath := filepath.Join(opts.OutDir, "task-"+handoffID+".tar.gz")
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	res, err := Build(BuildOptions{
		OutPath:   bundlePath,
		HandoffID: handoffID,
		Direction: DirectionUpload,
		Selection: sel,
		Origin: Origin{
			HostID:            localHostID,
			SessionID:         opts.SessionID,
			WorkspaceID:       sel.WorkspaceID,
			HeadTurnID:        opts.HeadTurnID,
			BoundarySequence:  opts.BoundarySequence,
			StorageGeneration: opts.StorageGeneration,
			CapturedAt:        now,
		},
		SessionDir:     sessionDir,
		TaskWindow:     taskWindow,
		CheckpointDirs: opts.Checkpoints,
		WorkspaceDir:   workspaceDir,
		Workspace:      winfo,
		CheckpointIDs:  opts.Checkpoints,
		ConfigDigest:   opts.ConfigDigest,
		Excludes:       excludes,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		return nil, err
	}
	return &UploadPlan{
		HandoffID:  handoffID,
		BundlePath: bundlePath,
		Manifest:   res.Manifest,
		Selection:  sel,
		StagingDir: staging,
		TaskWindow: taskWindow,
	}, nil
}

// CommitUpload records the outcome on the source host. Only a confirmed
// successful import on the destination may pass ok=true, which flips the
// single-writer owner to the cloud and records the identity binding.
func (p *Planner) CommitUpload(plan *UploadPlan, cloudHostID, cloudSessionID string, ok bool, detail string) error {
	if plan == nil || plan.Manifest == nil {
		return errors.New("cloudsync: nil upload plan")
	}
	rec := TransferRecord{
		HandoffID:    plan.Manifest.HandoffID,
		Direction:    DirectionUpload,
		At:           time.Now().UTC(),
		BundleDigest: plan.Manifest.Digest,
		Status:       "failed",
		Detail:       detail,
	}
	if ok {
		rec.Status = "ok"
		m := OriginMapping{
			LocalHostID:    p.Store.LocalHostID(),
			LocalSessionID: plan.Selection.SessionID,
			CloudHostID:    cloudHostID,
			CloudSessionID: cloudSessionID,
			LastHandoffID:  plan.Manifest.HandoffID,
		}
		if err := p.Store.Bind(m); err != nil {
			return err
		}
		if err := p.Store.SetOwner(plan.Selection.SessionID, OwnerCloud); err != nil {
			return err
		}
	}
	return p.Store.RecordTransfer(plan.Selection.SessionID, rec)
}

// ApplyOptions describes an incoming bundle on the destination host.
type ApplyOptions struct {
	BundlePath string
	// ExtractRoot is a staging directory; the verified payload is written here
	// before any workspace materialisation.
	ExtractRoot string
	// WorkspaceDest is where a git workspace should be cloned. Empty skips it.
	WorkspaceDest string
	// NewOwner is the writer after a successful import: cloud on upload
	// acceptance, local on a download back to the workstation.
	NewOwner Owner
	// DestinationVector is what this host already has for the logical task.
	// A fresh destination passes the zero value.
	DestinationVector VersionVector
	// PinOnApply records the selection locally when importing for the first time.
	PinOnApply bool
	// SessionRoot publishes the unpacked session/ payload into a sessions-v4
	// root so the destination serve can open it. Empty skips installation.
	SessionRoot  string
	ForceSession bool
	Ctx          context.Context
}

// ApplyResult reports what was imported.
type ApplyResult struct {
	Manifest            *Manifest
	ExtractDir          string
	HandoffID           string
	InstalledSessionDir string
}

// ApplyIncoming is the destination side of a handoff. It is deliberately
// fail-closed: the manifest is verified, the version vector is checked, and
// every file checksum is validated before the sync state is touched.
func (p *Planner) ApplyIncoming(opts ApplyOptions) (*ApplyResult, error) {
	if p.Store == nil {
		return nil, errors.New("cloudsync: nil store")
	}
	opened, err := OpenBundle(opts.BundlePath)
	if err != nil {
		return nil, err
	}
	m := opened.Manifest
	incoming := VersionVector{
		HostID:            m.Origin.HostID,
		SessionID:         m.Origin.SessionID,
		BoundarySequence:  m.Origin.BoundarySequence,
		StorageGeneration: m.Origin.StorageGeneration,
	}
	if err := CheckHandoff(incoming, opts.DestinationVector); err != nil {
		return nil, err
	}
	if opts.NewOwner != OwnerLocal && opts.NewOwner != OwnerCloud {
		return nil, fmt.Errorf("cloudsync: invalid new owner %q", opts.NewOwner)
	}
	if strings.TrimSpace(opts.ExtractRoot) == "" {
		return nil, errors.New("cloudsync: empty extraction root")
	}
	extractDir := filepath.Join(opts.ExtractRoot, "task-"+m.HandoffID)
	if err := os.RemoveAll(extractDir); err != nil {
		return nil, err
	}
	if err := opened.Extract(extractDir); err != nil {
		return nil, err
	}
	if opts.WorkspaceDest != "" && m.Workspace.IsGit {
		if err := ApplyWorkspace(filepath.Join(extractDir, "workspace"), opts.WorkspaceDest, m.Workspace); err != nil {
			return nil, err
		}
	}
	var installedSession string
	sessionPayload := filepath.Join(extractDir, "session")
	if _, statErr := os.Stat(filepath.Join(sessionPayload, "manifest.json")); statErr == nil {
		if strings.TrimSpace(opts.SessionRoot) != "" {
			ctx := opts.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			installed, installErr := InstallSessionExport(ctx, sessionPayload, opts.SessionRoot, m.Selection.SessionID, opts.ForceSession)
			if installErr != nil {
				return nil, installErr
			}
			installedSession = installed
		}
	}
	if opts.PinOnApply {
		if err := p.Store.Pin(m.Selection.SessionID, m.Selection.Title, m.Selection.WorkspaceID, m.Selection.Tasks); err != nil {
			return nil, err
		}
	} else if _, known := p.Store.Get(m.Selection.SessionID); !known {
		// An import always establishes the identity, even when the operator
		// did not ask to overwrite an existing local selection.
		if err := p.Store.Pin(m.Selection.SessionID, m.Selection.Title, m.Selection.WorkspaceID, m.Selection.Tasks); err != nil {
			return nil, err
		}
	}
	if err := p.Store.SetOwner(m.Selection.SessionID, opts.NewOwner); err != nil {
		return nil, err
	}
	direction := DirectionUpload
	if opts.NewOwner == OwnerLocal {
		direction = DirectionDownload
	}
	if err := p.Store.RecordTransfer(m.Selection.SessionID, TransferRecord{
		HandoffID:    m.HandoffID,
		Direction:    direction,
		At:           time.Now().UTC(),
		BundleDigest: m.Digest,
		Status:       "ok",
	}); err != nil {
		return nil, err
	}
	return &ApplyResult{Manifest: m, ExtractDir: extractDir, HandoffID: m.HandoffID, InstalledSessionDir: installedSession}, nil
}

// TaskScopeNote describes the effective scope of one handoff. Task-level
// slicing is applied by PrepareUpload and recorded in Manifest.TaskWindow.
func TaskScopeNote(sel Selection) string {
	if len(sel.Tasks) == 0 {
		return "whole session"
	}
	return fmt.Sprintf("session with %d selected task(s): only matching durable turns are packed", len(sel.Tasks))
}

// WindowNote renders a sealed task window for CLI output.
func WindowNote(w *TaskWindow) string {
	if w == nil {
		return "no session payload"
	}
	if !w.Sliced {
		return fmt.Sprintf("whole session (%d commits, %s)", w.TotalCommits, w.Reason)
	}
	return fmt.Sprintf("sliced to %d/%d turns, %d/%d events, seq %d-%d",
		w.KeptCommits, w.TotalCommits, w.KeptEvents, w.TotalEvents, w.FirstSeq, w.LastSeq)
}
