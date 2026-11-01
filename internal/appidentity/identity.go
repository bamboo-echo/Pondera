package appidentity

const (
	// AppUserModelID belongs to Desktop, independently of installed Studio versions.
	// Keep it stable across upgrades and aligned with the Electron shell.
	AppUserModelID = "io.pondera.desktop"
	DisplayName    = "Pondera"

	legacyAppUserModelID      = "Pondera"
	studioAppUserModelID      = "io.pondera.studio"
	legacyTauriAppUserModelID = "dev.pondera.desktop"
)
