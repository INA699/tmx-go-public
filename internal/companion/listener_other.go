// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

//go:build !windows

package companion

import "net"

// No unauthenticated HTTP claim can establish listener identity. Until a native
// socket-owner verifier is implemented here, authenticated calls fail closed.
func verifyCompanionConnection(net.Conn) error { return ErrListenerIdentity }
