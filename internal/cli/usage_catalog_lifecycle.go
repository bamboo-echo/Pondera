package cli

import (
	"context"
	"time"

	"pondera/internal/config"
	"pondera/internal/stats"
)

func closeCLIUsageCatalogs() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = stats.Flush(ctx, config.StatsDir())
	_ = stats.CloseUsageCatalogs(ctx)
	_ = stats.CloseRecordDispatchers(ctx)
}
