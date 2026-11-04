// Package sidecar is the host half of Extension Protocol v2: it spawns
// extension sidecar processes, runs the initialize handshake, serves their
// Extension → Host calls (content reads, UI, provider streams), and owns
// their bounded shutdown and crash supervision.
//
// AUTHORIZATION INVARIANT: a sidecar may only ever be launched for a plugin
// package that is present in the pluginpkg installed state
// (<Pondera home>/plugin-packages.json) AND currently enabled. The launch
// API (Manager.StartPackages) takes the pluginpkg installed state as its only
// input — there is no way to point it at an arbitrary binary or at a runtime
// declared by project config. Project configuration can declare MCP servers,
// hooks, and skills, but it can never declare a v2 runtime; keeping this
// invariant by construction is why StartPackages accepts a home directory and
// loads the state itself instead of accepting caller-supplied command specs.
//
// ENVIRONMENT CONTRACT: a sidecar process receives a deny-by-default
// environment: a closed whitelist of system essentials (executable lookup,
// temp/cache dirs, system roots — see inheritedRuntimeEnvKeys), plus the
// variables its manifest declares under runtime.env, plus PONDERA_PLUGIN_ROOT /
// PONDERA_PLUGIN_NAME / PONDERA_PLUGIN_VERSION. Host credentials are never
// inherited: PONDERA_* and *_KEY / *_TOKEN / *_SECRET / *_PASSWORD /
// AUTHORIZATION names are removed, empty declarations are ignored, and ordinary
// host variables are absent unless declared. A sidecar still runs with the
// user's authority (it can read the session, files, and workspace), so it
// remains a full-trust installation decision; only environment inheritance is
// narrowed. Sidecar stderr is redacted in this process layer; provider errors,
// structured UI, and interceptor reasons are redacted again by their host-side
// consumers. Ordinary provider/model content remains unchanged as product data.
package sidecar
