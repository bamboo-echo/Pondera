package main

type requiredDesktopAsset struct {
	group    string
	key      string
	filename string
}

var (
	requiredDesktopUpdaterAssets = []requiredDesktopAsset{
		{group: "platforms", key: "darwin-arm64", filename: "Pondera-darwin-arm64.zip"},
		{group: "platforms", key: "darwin-amd64", filename: "Pondera-darwin-amd64.zip"},
		{group: "platforms", key: "windows-amd64", filename: "Pondera-windows-amd64-installer.exe"},
		{group: "platforms", key: "windows-arm64", filename: "Pondera-windows-arm64-installer.exe"},
		{group: "platforms", key: "linux-amd64", filename: "Pondera-linux-amd64.tar.gz"},
		{group: "native_packages", key: "linux-amd64", filename: "Pondera-linux-amd64.deb"},
	}
	legacyDesktopDownloadAssets = []requiredDesktopAsset{
		{group: "downloads", key: "Pondera-darwin-universal.dmg", filename: "Pondera-darwin-universal.dmg"},
		{group: "downloads", key: "Pondera-windows-amd64.zip", filename: "Pondera-windows-amd64.zip"},
	}
	requiredDesktopDownloadAssets = append(append([]requiredDesktopAsset(nil), legacyDesktopDownloadAssets...),
		requiredDesktopAsset{group: "downloads", key: "Pondera-darwin-arm64.dmg", filename: "Pondera-darwin-arm64.dmg"},
		requiredDesktopAsset{group: "downloads", key: "Pondera-darwin-amd64.dmg", filename: "Pondera-darwin-amd64.dmg"},
	)
)
