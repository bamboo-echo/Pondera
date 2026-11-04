package patch

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Plan is a validated, previewed patch waiting for explicit confirmation.
// Persisting it lets the desktop shell split chat → preview → apply across
// separate RPC calls without trusting a renderer-supplied operation list.
type Plan struct {
	ID                   string       `json:"id"`
	CreatedAt            string       `json:"created_at"`
	Intent               string       `json:"intent"`
	Reply                string       `json:"reply"`
	Ops                  []ResolvedOp `json:"ops"`
	Diff                 []DiffEntry  `json:"diff"`
	BaseHash             string       `json:"base_hash"`
	CredentialRevision   string       `json:"credential_revision"`
	CommentWarning       string       `json:"comment_warning"`
	RequiresConfirmation bool         `json:"requires_confirmation"`
}

// NewPlanID returns a random 16-byte hex id.
func NewPlanID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// BuildPlan assembles the preview object from validated ops.
func BuildPlan(id string, env *Envelope, ops []ResolvedOp, baseHash, credentialRevision string) *Plan {
	p := &Plan{
		ID: id, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Intent: env.Intent, Reply: env.Reply, Ops: ops,
		Diff: RenderDiff(ops), BaseHash: baseHash,
		CredentialRevision: credentialRevision, CommentWarning: CommentWarning,
	}
	for _, op := range ops {
		if op.Risk != "benign" {
			p.RequiresConfirmation = true
		}
	}
	return p
}
