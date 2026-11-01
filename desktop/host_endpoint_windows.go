//go:build windows

package main

import (
	"os"
	"pondera/desktop/internal/instanceidentity"
	"pondera/internal/config"
)

func startUpdateEndpoint() (func(), error) {
	if !hostRPCRequested(os.Args[1:]) {
		return func() {}, nil
	}
	return instanceidentity.ListenEndpoint(instanceidentity.ForHome(config.PonderaHomeDir()))
}
