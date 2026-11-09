// Package supervisor implements the local control service for a Togavsu
// Compose stack: signed-update orchestration, encrypted backups, module
// start/stop, and the Caddy upstream switch.
//
// The core process never receives the Docker socket. It calls this service
// with the installer-generated bearer token. Product acceptance for a fresh
// install, a zero-downtime swap, and a full firm restore stays pending until
// the core, identity, and signed images exist. See docs/platform/pending.md.
package supervisor
