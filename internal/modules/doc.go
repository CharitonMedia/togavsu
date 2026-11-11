// Package modules is the module runtime (PRD section 9): signed packages, the manifest,
// permission approval, scoped tokens, module schemas with checked migrations, include=
// reads and published views, sandboxed frames with context tokens, domain replacement,
// signed event webhooks and AI calls through the core's AI settings.
//
// Access stays in the service layer. include= and published views apply walls, roles and
// attorney-only columns through the core record each module row points to; a module's
// call carrying a context token runs as the viewer (Delegate). Everything a module owns
// in the database is read and changed as the module's own role, so nothing it put in its
// schema ever runs with the core's privileges.
//
// The core wires the package (integrate-extensions): Migrate on the plain pool, New with
// identity, settings, help, the dashboard catalog and the supervisor, Handler.Routes
// beside the other domains, Service.Router and Handler.Include around the core handler,
// Start at boot and Run beside the other background loops.
package modules
