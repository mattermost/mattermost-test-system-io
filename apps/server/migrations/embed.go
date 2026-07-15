// Package migrations embeds the .sql files in this directory into the binary
// so production deploys don't depend on a mounted migrations directory. The
// tsio server and tsioctl both drive golang-migrate from FS via the iofs
// source driver — see internal/db/migrate.go.
package migrations

import "embed"

// FS holds every .sql file in this directory, embedded into the server binary
// at compile time. Consumed by internal/db via the golang-migrate iofs source
// driver so migrations ship inside the binary with no on-disk dependency.
//
//go:embed *.sql
var FS embed.FS
