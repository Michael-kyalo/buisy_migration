// Package migration embeds the versioned SQL that drives the migration, so a
// built binary (and the ECS image) always carries the SQL it was built with.
package migration

import "embed"

// SQL holds sql/bootstrap.sql, transforms/{common,<target>}/, validate/{common,<target>}/ and profile/.
//
//go:embed sql/*.sql transforms/*/*.sql validate/*/*.sql profile/mysql/*.sql profile/extract/*.sql profile/pg/*.sql
var SQL embed.FS
