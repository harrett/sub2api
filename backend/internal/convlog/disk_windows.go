//go:build windows

package convlog

// diskFreeBytes 在 Windows 上返回 -1（未知）。此时磁盘水位保护降级为只依赖
// spool_max_bytes 的总量上限——Windows 不是本项目的生产部署目标，不值得为它
// 引入 syscall 依赖。
func diskFreeBytes(string) int64 { return -1 }
