package convlog

import (
	"errors"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// AdminHandler 提供会话数据留存的配置与 Beta 风控搜索接口。
type AdminHandler struct{ service *Service }

func NewAdminHandler(service *Service) *AdminHandler {
	return &AdminHandler{service: service}
}

type settingsResponse struct {
	*Settings
	SecretConfigured bool `json:"secret_configured"`
}

// GetConfig 返回当前配置（密钥已脱敏）。
func (h *AdminHandler) GetConfig(c *gin.Context) {
	store := h.settingStore()
	if store == nil {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_unavailable", "会话数据留存未启用"))
		return
	}
	settings, err := store.Get(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settingsResponse{
		Settings:         settings,
		SecretConfigured: store.SecretConfigured(c.Request.Context()),
	})
}

// UpdateConfig 保存配置并立即生效。
func (h *AdminHandler) UpdateConfig(c *gin.Context) {
	store := h.settingStore()
	if store == nil {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_unavailable", "会话数据留存未启用"))
		return
	}
	var request Settings
	if err := c.ShouldBindJSON(&request); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_invalid_config", "会话数据留存配置无效"))
		return
	}
	saved, err := store.Update(c.Request.Context(), request)
	if err != nil {
		if errors.Is(err, ErrEncryptionKeyNotConfigured) {
			response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_encryption_key_required",
				"未配置固定的密钥加密 key，保存的密文在重启后将无法解密"))
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	h.service.ApplySettings(c.Request.Context())
	response.Success(c, settingsResponse{
		Settings:         saved,
		SecretConfigured: store.SecretConfigured(c.Request.Context()),
	})
}

// TestConnection 用提交的配置试建一次对象存储客户端。
func (h *AdminHandler) TestConnection(c *gin.Context) {
	store := h.settingStore()
	if store == nil {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_unavailable", "会话数据留存未启用"))
		return
	}
	var request Settings
	if err := c.ShouldBindJSON(&request); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_invalid_config", "会话数据留存配置无效"))
		return
	}
	if err := store.TestConnection(c.Request.Context(), request, h.service.Factory()); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_s3_unreachable", err.Error()))
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// GetRuntime 返回队列深度、丢弃数、spool 占用、磁盘余量等运行态。
func (h *AdminHandler) GetRuntime(c *gin.Context) {
	if h.service == nil {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_unavailable", "会话数据留存未启用"))
		return
	}
	response.Success(c, h.service.Runtime())
}

type searchResponse struct {
	Records []IndexRow      `json:"records"`
	Summary *AccountSummary `json:"summary,omitempty"`
	Filter  searchEcho      `json:"filter"`
}

type searchEcho struct {
	AccountID int64     `json:"account_id"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Keyword   string    `json:"keyword,omitempty"`
	Limit     int       `json:"limit"`
}

// SearchRecords 是 Beta 风控搜索：必须指定账号池账号与时间范围。
func (h *AdminHandler) SearchRecords(c *gin.Context) {
	if h.service == nil {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_unavailable", "会话数据留存未启用"))
		return
	}
	filter, err := parseSearchFilter(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	records, summary, err := h.service.Search(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, translateSearchError(err))
		return
	}
	filter.Normalize()
	response.Success(c, searchResponse{
		Records: records,
		Summary: summary,
		Filter: searchEcho{
			AccountID: filter.AccountID,
			Start:     filter.Start,
			End:       filter.End,
			Keyword:   filter.Keyword,
			Limit:     filter.Limit,
		},
	})
}

// GetFullRecord 取回单条完整会话（从本地 spool 或对象存储）。
func (h *AdminHandler) GetFullRecord(c *gin.Context) {
	if h.service == nil {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_unavailable", "会话数据留存未启用"))
		return
	}
	requestID := strings.TrimSpace(c.Param("request_id"))
	if requestID == "" {
		response.ErrorFrom(c, infraerrors.BadRequest("conversation_capture_invalid_request_id", "request_id 不能为空"))
		return
	}
	record, err := h.service.FetchFullRecord(c.Request.Context(), requestID)
	if err != nil {
		if errors.Is(err, ErrRecordNotFound) {
			response.ErrorFrom(c, infraerrors.NotFound("conversation_capture_record_not_found", err.Error()))
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"request_id": requestID, "record": record})
}

func (h *AdminHandler) settingStore() *SettingStore {
	if h == nil || h.service == nil {
		return nil
	}
	return h.service.Settings()
}

func parseSearchFilter(c *gin.Context) (SearchFilter, error) {
	filter := SearchFilter{Keyword: c.Query("keyword")}

	accountID, err := strconv.ParseInt(strings.TrimSpace(c.Query("account_id")), 10, 64)
	if err != nil || accountID <= 0 {
		return filter, infraerrors.BadRequest("conversation_capture_account_required",
			"必须指定账号池账号后再检索")
	}
	filter.AccountID = accountID

	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		userID, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || userID <= 0 {
			return filter, infraerrors.BadRequest("conversation_capture_invalid_user", "user_id 无效")
		}
		filter.UserID = &userID
	}

	if filter.Start, err = parseTimeQuery(c, "start"); err != nil {
		return filter, err
	}
	if filter.End, err = parseTimeQuery(c, "end"); err != nil {
		return filter, err
	}

	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit <= 0 {
			return filter, infraerrors.BadRequest("conversation_capture_invalid_limit", "limit 无效")
		}
		filter.Limit = limit
	}

	filter.Normalize()
	return filter, nil
}

func parseTimeQuery(c *gin.Context, key string) (time.Time, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, infraerrors.BadRequest("conversation_capture_invalid_time",
			key+" 必须是 RFC3339 时间")
	}
	return parsed.UTC(), nil
}

func translateSearchError(err error) error {
	switch {
	case errors.Is(err, ErrAccountRequired):
		return infraerrors.BadRequest("conversation_capture_account_required", "必须指定账号池账号后再检索")
	case errors.Is(err, ErrTimeRangeInvalid):
		return infraerrors.BadRequest("conversation_capture_invalid_range", "开始时间必须早于结束时间")
	case errors.Is(err, ErrRangeTooWide):
		return infraerrors.BadRequest("conversation_capture_range_too_wide", "检索时间跨度不能超过 30 天")
	default:
		return err
	}
}
