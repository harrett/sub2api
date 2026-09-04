//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// newOpenAIWildcardTokenPricingResolverForTest 构造一张只覆盖文本模型的通配 token 价卡
// （gpt-*，未配置任何图片输入/输出单价），与生产上"给文本模型配了通配价卡"的形态一致。
func newOpenAIWildcardTokenPricingResolverForTest(t *testing.T, groupID int64, prefix string) *ModelPricingResolver {
	t.Helper()
	inputPrice := 5e-6
	cache := newEmptyChannelCache()
	gpKey := channelGroupPlatformKey{groupID: groupID}
	cache.wildcardByGroupPlatform[gpKey] = append(cache.wildcardByGroupPlatform[gpKey], &wildcardPricingEntry{
		prefix: prefix,
		pricing: &ChannelModelPricing{
			BillingMode: BillingModeToken,
			Models:      []string{prefix + "*"},
			InputPrice:  &inputPrice,
		},
	})
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = ""
	cache.loadedAt = time.Now()
	cs := &ChannelService{}
	cs.cache.Store(cache)
	return NewModelPricingResolver(cs, NewBillingService(&config.Config{}, nil))
}

func imageBillingTestGroup(groupID int64) *Group {
	price2K := 0.201
	return &Group{
		ID:                   groupID,
		RateMultiplier:       1,
		AllowImageGeneration: true,
		ImageRateIndependent: true,
		ImageRateMultiplier:  5,
		ImagePrice2K:         &price2K,
	}
}

// 通配 token 价卡不得把生图请求拖进 token 计费：applyTokenOverrides 会把图片输入/输出
// 单价一并归零，一张图只按文本输入价收几厘钱，而上游按张计费。
func TestOpenAIRecordUsage_WildcardTokenCardDoesNotDowngradeImageBilling(t *testing.T) {
	groupID := int64(9101)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.resolver = newOpenAIWildcardTokenPricingResolverForTest(t, groupID, "gpt-")

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:  "img_wildcard_token_card",
			Usage:      OpenAIUsage{InputTokens: 1609, OutputTokens: 429, ImageInputTokens: 1505, ImageOutputTokens: 429},
			Model:      "gpt-image-2",
			Duration:   time.Second,
			ImageCount: 1,
			ImageSize:  ImageBillingSize2K,
		},
		APIKey:  &APIKey{ID: 9102, GroupID: i64p(groupID), Group: imageBillingTestGroup(groupID)},
		User:    &User{ID: 9103},
		Account: &Account{ID: 9104},
	})
	require.NoError(t, err)

	log := usageRepo.lastLog
	require.NotNil(t, log)
	require.NotNil(t, log.BillingMode)
	require.Equal(t, string(BillingModeImage), *log.BillingMode)
	require.Equal(t, 5.0, log.RateMultiplier)
	require.InDelta(t, 0.201, log.TotalCost, 1e-12)
	require.InDelta(t, 1.005, log.ActualCost, 1e-12)
}

// 管理员为该模型精确配置的 token 价卡仍然生效——这条逃生门是有意保留的
// （例如按 token 计费的 Gemini 生图模型）。
func TestOpenAIRecordUsage_ExactTokenCardStillBillsImagesByToken(t *testing.T) {
	groupID := int64(9201)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, groupID, "gpt-image-2")

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:  "img_exact_token_card",
			Usage:      OpenAIUsage{InputTokens: 1000, OutputTokens: 400, ImageOutputTokens: 400},
			Model:      "gpt-image-2",
			Duration:   time.Second,
			ImageCount: 1,
			ImageSize:  ImageBillingSize2K,
		},
		APIKey:  &APIKey{ID: 9202, GroupID: i64p(groupID), Group: imageBillingTestGroup(groupID)},
		User:    &User{ID: 9203},
		Account: &Account{ID: 9204},
	})
	require.NoError(t, err)

	log := usageRepo.lastLog
	require.NotNil(t, log)
	require.NotNil(t, log.BillingMode)
	require.Equal(t, string(BillingModeToken), *log.BillingMode)
}

// newOpenAIExactCardResolverForTest 构造一张精确列出该模型的价卡，价格字段由调用方决定。
func newOpenAIExactCardResolverForTest(t *testing.T, groupID int64, model string, card *ChannelModelPricing) *ModelPricingResolver {
	t.Helper()
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, model: normalizeChannelPricingModelName(model)}] = card
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = ""
	cache.loadedAt = time.Now()
	cs := &ChannelService{}
	cs.cache.Store(cache)
	return NewModelPricingResolver(cs, NewBillingService(&config.Config{}, nil))
}

// 渠道编辑页把"支持的模型清单"和价格放在同一张卡里，价格全留空是常态。
// 这种空壳卡不携带任何计费意图，不得把生图请求拖进 token 计费。
func TestOpenAIRecordUsage_EmptyModelListCardDoesNotDowngradeImageBilling(t *testing.T) {
	groupID := int64(9401)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.resolver = newOpenAIExactCardResolverForTest(t, groupID, "gpt-image-2", &ChannelModelPricing{
		Models: []string{"gpt-5.6", "gpt-image-1", "gpt-image-2", "o3-mini"},
	})

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:  "img_empty_model_list_card",
			Usage:      OpenAIUsage{InputTokens: 1609, OutputTokens: 429, ImageInputTokens: 1505, ImageOutputTokens: 429},
			Model:      "gpt-image-2",
			Duration:   time.Second,
			ImageCount: 1,
			ImageSize:  ImageBillingSize2K,
		},
		APIKey:  &APIKey{ID: 9402, GroupID: i64p(groupID), Group: imageBillingTestGroup(groupID)},
		User:    &User{ID: 9403},
		Account: &Account{ID: 9404},
	})
	require.NoError(t, err)

	log := usageRepo.lastLog
	require.NotNil(t, log)
	require.NotNil(t, log.BillingMode)
	require.Equal(t, string(BillingModeImage), *log.BillingMode)
	require.Equal(t, 5.0, log.RateMultiplier)
	require.InDelta(t, 1.005, log.ActualCost, 1e-12)
}

// 配了文本价但没配图片输出价：按 token 计费会把出图那部分白送，同样不采纳。
func TestOpenAIRecordUsage_TokenCardWithoutImageOutputPriceKeepsPerImageBilling(t *testing.T) {
	groupID := int64(9501)
	inputPrice := 5e-6
	outputPrice := 20e-6
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.resolver = newOpenAIExactCardResolverForTest(t, groupID, "gpt-image-2", &ChannelModelPricing{
		BillingMode: BillingModeToken,
		Models:      []string{"gpt-image-2"},
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	})

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:  "img_token_card_missing_image_output_price",
			Usage:      OpenAIUsage{InputTokens: 1609, OutputTokens: 429, ImageOutputTokens: 429},
			Model:      "gpt-image-2",
			Duration:   time.Second,
			ImageCount: 1,
			ImageSize:  ImageBillingSize2K,
		},
		APIKey:  &APIKey{ID: 9502, GroupID: i64p(groupID), Group: imageBillingTestGroup(groupID)},
		User:    &User{ID: 9503},
		Account: &Account{ID: 9504},
	})
	require.NoError(t, err)

	log := usageRepo.lastLog
	require.NotNil(t, log)
	require.NotNil(t, log.BillingMode)
	require.Equal(t, string(BillingModeImage), *log.BillingMode)
}

func TestChannelPricingConfiguresPrice(t *testing.T) {
	price := 1e-6
	require.False(t, channelPricingConfiguresPrice(nil))
	require.False(t, channelPricingConfiguresPrice(&ChannelModelPricing{Models: []string{"gpt-image-2"}}))
	// 只有 min/max 没有价格的空区间同样不算配价
	require.False(t, channelPricingConfiguresPrice(&ChannelModelPricing{Intervals: []PricingInterval{{MinTokens: 0}}}))
	require.True(t, channelPricingConfiguresPrice(&ChannelModelPricing{InputPrice: &price}))
	require.True(t, channelPricingConfiguresPrice(&ChannelModelPricing{ImageOutputPrice: &price}))
	require.True(t, channelPricingConfiguresPrice(&ChannelModelPricing{Intervals: []PricingInterval{{InputPrice: &price}}}))
	require.True(t, channelPricingConfiguresPrice(&ChannelModelPricing{
		TimePricing: &ChannelTimePricing{Periods: []ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "23:59", Multiplier: 2}}},
	}))
}

func TestModelPricingResolver_ReportsWildcardMatch(t *testing.T) {
	groupID := int64(9301)
	resolver := newOpenAIWildcardTokenPricingResolverForTest(t, groupID, "gpt-")

	wildcardHit := resolver.Resolve(context.Background(), PricingInput{Model: "gpt-image-2", GroupID: &groupID})
	require.Equal(t, PricingSourceChannel, wildcardHit.Source)
	require.Equal(t, BillingModeToken, wildcardHit.Mode)
	require.True(t, wildcardHit.WildcardMatch)
	require.False(t, mediaTokenBillingExplicit(wildcardHit))

	imageOutputPrice := 40e-6
	exactHit := resolver.Resolve(context.Background(), PricingInput{Model: "gpt-image-2", GroupID: &groupID, Group: &Group{
		ID: groupID,
		ModelPricing: []ChannelModelPricing{
			{Models: []string{"gpt-image-2"}, BillingMode: BillingModeToken, ImageOutputPrice: &imageOutputPrice},
		},
	}})
	require.Equal(t, PricingSourceGroup, exactHit.Source)
	require.False(t, exactHit.WildcardMatch)
	require.True(t, mediaTokenBillingExplicit(exactHit))
	require.True(t, imageTokenBillingExplicit(exactHit))
}

func TestMatchGroupModelPricing_ReportsWildcardMatch(t *testing.T) {
	group := &Group{ModelPricing: []ChannelModelPricing{
		{ID: 1, Models: []string{"gpt-*"}},
		{ID: 2, Models: []string{"gpt-image-2"}},
	}}

	exact, wildcard := matchGroupModelPricing(group, "gpt-image-2")
	require.NotNil(t, exact)
	require.Equal(t, int64(2), exact.ID)
	require.False(t, wildcard)

	fuzzy, wildcard := matchGroupModelPricing(group, "gpt-5.1")
	require.NotNil(t, fuzzy)
	require.Equal(t, int64(1), fuzzy.ID)
	require.True(t, wildcard)

	missing, wildcard := matchGroupModelPricing(group, "claude-opus-4")
	require.Nil(t, missing)
	require.False(t, wildcard)
}

func TestGetChannelModelPricingMatch_DistinguishesWildcard(t *testing.T) {
	ch := Channel{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{
			{ID: 200, Platform: "anthropic", Models: []string{"claude-*"}, InputPrice: testPtrFloat64(10e-6)},
			{ID: 100, Platform: "anthropic", Models: []string{"claude-opus-4"}, InputPrice: testPtrFloat64(15e-6)},
		},
	}
	svc := newTestChannelService(makeStandardRepo(ch, map[int64]string{10: "anthropic"}))

	pricing, wildcard := svc.GetChannelModelPricingMatch(context.Background(), 10, "claude-opus-4")
	require.NotNil(t, pricing)
	require.Equal(t, int64(100), pricing.ID)
	require.False(t, wildcard)

	pricing, wildcard = svc.GetChannelModelPricingMatch(context.Background(), 10, "claude-sonnet-4")
	require.NotNil(t, pricing)
	require.Equal(t, int64(200), pricing.ID)
	require.True(t, wildcard)

	pricing, wildcard = svc.GetChannelModelPricingMatch(context.Background(), 10, "gpt-5.1")
	require.Nil(t, pricing)
	require.False(t, wildcard)
}
