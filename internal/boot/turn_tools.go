package boot

import (
	"pondera/internal/agent"
	"pondera/internal/tool"
)

func registerInteractiveAgentTools(reg *tool.Registry) {
	reg.Add(agent.NewAskTool())
}
