package cli

import (
	"time"

	"pondera/internal/control"
	"pondera/internal/memory"
)

func renderMemory(width int, set *memory.Set) string {
	return viewProtectLines(control.RenderMemorySummary(set, time.Now().UTC()), width)
}
