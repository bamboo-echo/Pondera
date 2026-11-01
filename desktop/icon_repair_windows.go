//go:build windows

package main

import (
	"os"

	"pondera/internal/appidentity"
	"pondera/internal/installlayout"
)

func repairDesktopIconIntegration() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root, err := installlayout.ResolveInstallRoot(executable)
	if err != nil {
		return err
	}
	return appidentity.RepairOwnedShortcuts(root)
}
