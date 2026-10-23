//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestForwardResultBillingModel_NormalizesKiroModels(t *testing.T) {
	tests := []struct {
		name           string
		requestedModel string
		upstreamModel  string
		want           string
	}{
		{
			name:           "kiro claude sonnet 4.6 uses pricing key format",
			requestedModel: "claude-sonnet-4-6",
			upstreamModel:  "claude-sonnet-4.6",
			want:           "claude-sonnet-4-6",
		},
		{
			name:           "falls back to upstream when requested model empty",
			requestedModel: "",
			upstreamModel:  "claude-haiku-4-5",
			want:           "claude-haiku-4-5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, forwardResultBillingModel(tt.requestedModel, tt.upstreamModel))
		})
	}
}

func TestGatewayServiceRecordUsage_NormalizesKiroBillingModel(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)
	svc.billingService = NewBillingService(svc.cfg, &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"claude-sonnet-4-6": {
				InputCostPerToken:  2.5e-6,
				OutputCostPerToken: 10e-6,
			},
		},
	})

	expectedCost, err := svc.billingService.CalculateCost("claude-sonnet-4-6", UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_kiro_billing_normalized",
			Usage: ClaudeUsage{
				InputTokens:  20,
				OutputTokens: 10,
			},
			Model:         "claude-sonnet-4-6",
			UpstreamModel: "claude-sonnet-4.6",
			Duration:      time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: &Account{ID: 701, Platform: PlatformKiro},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "claude-sonnet-4-6", usageRepo.lastLog.Model)
	require.Equal(t, "claude-sonnet-4-6", usageRepo.lastLog.RequestedModel)
	require.NotNil(t, usageRepo.lastLog.UpstreamModel)
	require.Equal(t, "claude-sonnet-4.6", *usageRepo.lastLog.UpstreamModel)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, expectedCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
}

func TestGatewayServiceRecordUsage_KiroUnknownPricingFallsBackToConservativeCost(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	expectedCost, err := svc.billingService.CalculateCost(kiroConservativeFallbackBillingModel, UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_kiro_auto_fallback_cost",
			Usage: ClaudeUsage{
				InputTokens:  20,
				OutputTokens: 10,
			},
			Model:         "auto",
			UpstreamModel: "auto",
			Duration:      time.Second,
		},
		APIKey:  &APIKey{ID: 601, Quota: 100},
		User:    &User{ID: 701},
		Account: &Account{ID: 801, Platform: PlatformKiro},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, expectedCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
}
