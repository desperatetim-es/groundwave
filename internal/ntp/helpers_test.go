// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package ntp

import "os"

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }
