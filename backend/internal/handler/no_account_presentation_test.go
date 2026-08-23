//go:build unit

package handler

// fork 本地测试。单独成文件，避免长期跟随 upstream rebase 时与其测试冲突。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// stubModelAvailabilityDiagnoser 让测试直接指定池子诊断结果。
type stubModelAvailabilityDiagnoser struct {
	diagnosis service.ModelAvailabilityDiagnosis
}

func (s stubModelAvailabilityDiagnoser) DiagnoseModelAvailabilityForPlatform(
	context.Context, *int64, string, string,
) service.ModelAvailabilityDiagnosis {
	return s.diagnosis
}

func newNoAccountTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func TestClassifyNoAccountErrorLabelsPoolExhaustionAsServerSide(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// diag 为 nil 走 upstream 的兜底分支，即线上最常见的那条
	// "Service temporarily unavailable"。
	c := newNoAccountTestContext()
	cls := classifyNoAccountErrorFromGin(c, nil, nil, "gpt-5.6-sol", "gpt-5.6-sol", service.PlatformOpenAI)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status, "池子耗尽可重试，必须保持 503")
	require.False(t, cls.ModelNotFound)
	require.Contains(t, cls.Message, "服务器侧问题")
	require.Contains(t, cls.Message, "与你的账户余额和套餐额度无关")
	// 这是我们的容量问题，绝不能引导用户去充值。
	require.NotContains(t, cls.Message, "升级套餐")
	require.NotContains(t, cls.Message, "充值")
}

func TestClassifyNoAccountErrorLabelsModelMisconfigurationAsServerSide(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(8)
	apiKey := &service.APIKey{GroupID: &groupID}
	diag := stubModelAvailabilityDiagnoser{diagnosis: service.ModelAvailabilityDiagnosis{
		HasAccountsInPool: true,
		HasModelSupport:   false,
	}}

	c := newNoAccountTestContext()
	cls := classifyNoAccountErrorFromGin(c, diag, apiKey, "gpt-5.6-sol", "gpt-5.6-sol", service.PlatformOpenAI)

	// 配置问题重试永远不会成功，必须保持 404 而不是被改成可重试的状态码。
	require.Equal(t, http.StatusNotFound, cls.Status)
	require.True(t, cls.ModelNotFound, "分类标记必须透传，调用方靠它决定后续行为")
	require.Equal(t, "model_not_found", cls.ErrType)
	require.Contains(t, cls.Message, "服务器侧问题")
	require.Contains(t, cls.Message, "服务端配置问题")
	// upstream 原始说明（含模型名）不能被指引覆盖掉。
	require.Contains(t, cls.Message, "gpt-5.6-sol")
}

func TestClassifyNoAccountErrorKeepsHealthyPoolOnFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 池子里有账号且支持该模型，只是当下全部不可调度（限流冷却 / 额度暂停）：
	// 走兜底 503，而不是误报成 404 配置问题。
	groupID := int64(8)
	apiKey := &service.APIKey{GroupID: &groupID}
	diag := stubModelAvailabilityDiagnoser{diagnosis: service.ModelAvailabilityDiagnosis{
		HasAccountsInPool: true,
		HasModelSupport:   true,
	}}

	c := newNoAccountTestContext()
	cls := classifyNoAccountErrorFromGin(c, diag, apiKey, "gpt-5.6-sol", "gpt-5.6-sol", service.PlatformOpenAI)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status)
	require.False(t, cls.ModelNotFound)
	require.Contains(t, cls.Message, "限流冷却或额度暂停")
}

func TestClassifyNoAccountErrorDoesNotLeakPoolInternals(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 调度器的 pool=/filtered: 摘要只进应用日志，不能出现在客户端响应里
	// （会泄露账号池规模与内部过滤原因）。
	c := newNoAccountTestContext()
	cls := classifyNoAccountErrorFromGin(c, nil, nil, "gpt-5.6-sol", "gpt-5.6-sol", service.PlatformOpenAI)

	for _, leak := range []string{"pool=", "filtered:", "quota_auto_pause", "selection_order", "runtime_blocked"} {
		require.NotContains(t, cls.Message, leak)
	}
}
