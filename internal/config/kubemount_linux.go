//go:build linux

package config

import "golang.org/x/sys/unix"

// platformReadOnlyMount reads the per-mount flags with statfs(2): since Linux
// 2.6.36 f_flags carries ST_RDONLY for a read-only mount, including a
// read-only bind mount over a writable filesystem (which is how the kubelet
// presents ConfigMap and Secret volumes to a container).
func platformReadOnlyMount(path string) (bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false, err
	}
	return st.Flags&unix.ST_RDONLY != 0, nil
}
