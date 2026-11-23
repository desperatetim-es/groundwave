// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package groundwave holds the license texts that live at the repository root,
// embedded so the program can show them in its Help menu without any files
// next to the executable.
package groundwave

import "embed"

// Legal is the program's own license and the third-party notices.
// THIRD_PARTY_LICENSES.txt is generated: run `go run ./tools/licenses` after
// changing go.mod.
//
//go:embed LICENSE THIRD_PARTY_LICENSES.txt
var Legal embed.FS
