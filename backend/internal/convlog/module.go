package convlog

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/wire"
)

// ProviderSet 是会话数据留存模块的装配集。对象存储工厂由 repository 层提供，
// 在 cmd/server 的注入器里补上，本包不反向依赖 repository。
var ProviderSet = wire.NewSet(
	ProvideBackupCredentials,
	NewSettingStore,
	NewRepository,
	NewService,
	NewAdminHandler,
)

// ProvideBackupCredentials 把备份服务当作凭证来源注入。写成显式 provider 而不是
// wire.Bind：绑定要求具体类型的 provider 与绑定同处一个 set，而 BackupService
// 由 service.ProviderSet 提供。
func ProvideBackupCredentials(backup *service.BackupService) BackupCredentials {
	return backup
}
