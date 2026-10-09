//go:build !linux

package config

// platformReadOnlyMount always reports false off Linux: the kubelet only
// projects volumes into Linux containers, so outside Linux there is nothing
// to exempt and the permissions advisory keeps firing as before.
func platformReadOnlyMount(string) (bool, error) { return false, nil }
