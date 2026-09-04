//go:build !windows

package convlog

import "syscall"

// diskFreeBytes 返回 path 所在文件系统的可用字节数；取不到时返回 -1（未知）。
func diskFreeBytes(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return -1
	}
	//nolint:unconvert // Bavail/Bsize 的具体整型宽度随平台不同，显式转换保证 64 位算术。
	return int64(stat.Bavail) * int64(stat.Bsize)
}
