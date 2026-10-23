//go:build android || ios

package core

import "errors"

// TunNeedsElevation: the mobile apps use the operating system's VPN API,
// which asks the user once for permission.
const TunNeedsElevation = false

func IsElevated() bool { return true }

// defaultTunDriver is replaced by the mobile bindings (package mobile).
func defaultTunDriver() TunDriver { return noTun{} }

type noTun struct{}

func (noTun) Prepare() error            { return nil }
func (noTun) Start(cfg TunConfig) error { return errors.New("no VPN interface") }
func (noTun) LUID() uint64              { return 0 }
func (noTun) Close()                    {}
