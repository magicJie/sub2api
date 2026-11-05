package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestModelQualityThresholdAndEvidence(t *testing.T) {
	expected := []ModelQualityCheck{
		{ID: "arithmetic", Expected: "983"}, {ID: "code", Expected: "17"},
		{ID: "logic", Expected: "C,A,D,B"}, {ID: "instruction", Expected: "probe-1"}, {ID: "data", Expected: "42"},
	}
	for _, tc := range []struct {
		name, text, status string
		threshold          int
		score              float64
	}{
		{"correct", `{"arithmetic":"983","code":"17","logic":"C,A,D,B","instruction":"probe-1","data":"42"}`, "passed", 80, 100},
		{"threshold-inclusive", `{"arithmetic":983,"code":17,"logic":"C,A,D,B","instruction":"probe-1","data":"wrong"}`, "passed", 80, 80},
		{"below-threshold", `{"arithmetic":"wrong","code":"17","logic":"C,A,D,B","instruction":"WRONG","data":"42"}`, "degraded", 80, 60},
		{"missing-answers", `{"arithmetic":"983"}`, "degraded", 80, 20},
		{"empty-object", `{}`, "inconclusive", 80, 0},
		{"refusal", `I cannot answer.`, "inconclusive", 80, 0},
		{"wrong-shape", `[]`, "inconclusive", 80, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := evaluateModelQuality(tc.text, expected, tc.threshold)
			require.Equal(t, tc.status, report.Status)
			require.Equal(t, tc.score, report.Score)
			if tc.status == "inconclusive" {
				require.NotEmpty(t, report.Error)
			}
		})
	}
}

func TestModelQualityFencedAnswersAndExactInstructions(t *testing.T) {
	expected := []ModelQualityCheck{{ID: "instruction", Expected: "probe-123"}}
	good := evaluateModelQuality("```json\n{\"instruction\":\"probe-123\"}\n```", expected, 100)
	require.Equal(t, "passed", good.Status)
	wrong := evaluateModelQuality(`{"instruction":"probe-123 extra"}`, expected, 100)
	require.Equal(t, "degraded", wrong.Status)
	require.Equal(t, "probe-123 extra", wrong.Checks[0].Actual)
}

func TestModelFingerprintRequiresIndependentValidEvidence(t *testing.T) {
	var sequence strings.Builder
	for i := range 300 {
		fmt.Fprintf(&sequence, "%d,", (i*i*17+i*29)%355+1)
	}
	response := func(text string) *http.Response {
		delta, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": text}, "finish_reason": "stop"}}})
		require.NoError(t, err)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + string(delta) + "\n\ndata: [DONE]\n\n"))}
	}
	for _, tc := range []struct {
		name, expected, status string
		valid                  int
	}{
		{"one-confident-answer", "", "inconclusive", 1},
		{"matching-model", "claude-opus-5-5", "match", 3},
		{"different-model", "gpt-5.5", "mismatch", 3},
		{"unknown-expected-model", "outside-bank", "inconclusive", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"base_url": "http://model.example", "api_key": "test-key"}, Extra: map[string]any{"openai_responses_supported": false}}
			upstream := &httpUpstreamRecorder{}
			for i := range 3 {
				text := "I decline"
				if i < tc.valid {
					text = sequence.String()
				}
				upstream.responses = append(upstream.responses, response(text))
			}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			svc := &AccountTestService{accountRepo: &upstreamModelMetadataRepoStub{account: account}, httpUpstream: upstream, cfg: cfg}
			result, err := svc.runModelDetection(context.Background(), &ScheduledTestPlan{AccountID: 1, ModelID: "gpt-5.5", TestType: "fingerprint", ExpectedModel: tc.expected})
			require.NoError(t, err)
			fp := result.Diagnostics.Fingerprint
			require.Equal(t, tc.status, fp.Status)
			require.Equal(t, tc.valid, fp.UsedOutputs)
			require.Greater(t, fp.Probability, .8)
			if tc.status == "match" {
				require.Equal(t, "success", result.Status)
			} else {
				require.Equal(t, "warning", result.Status)
			}
		})
	}
}

func TestModelQualityRejectsMappedMediaBeforeSpendingQuota(t *testing.T) {
	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-key", "base_url": "http://model.example", "model_mapping": map[string]any{"text-alias": "gpt-image-2"}},
	}
	upstream := &httpUpstreamRecorder{}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &AccountTestService{accountRepo: &upstreamModelMetadataRepoStub{account: account}, httpUpstream: upstream, cfg: cfg}
	result, err := svc.runModelDetection(context.Background(), &ScheduledTestPlan{AccountID: 1, ModelID: "text-alias", TestType: "quality", QualityThreshold: 80})
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Empty(t, upstream.requests)
}
