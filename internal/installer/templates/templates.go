// Package templates exposes the on-disk text templates the installer writes
// (config, service, plist, cloudflared config) as a single embed.FS, so the
// installer remains a single static binary.
package templates

import "embed"

// FS contains every .tmpl in this directory. Consumers Open by base name.
//
//go:embed *.tmpl
var FS embed.FS
