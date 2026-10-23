package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// testKiroAccountConnection 是 Kiro（fork）账号的连通性测试，由
// AccountTestService.TestAccountConnection 一行转发进入。
func (s *AccountTestService) testKiroAccountConnection(c *gin.Context, account *Account, modelID string) error {
	ctx := c.Request.Context()
	if s.httpUpstream == nil {
		return s.sendErrorAndEnd(c, "Kiro HTTP upstream not configured")
	}

	requestedModel := strings.TrimSpace(modelID)
	if requestedModel == "" {
		requestedModel = claude.DefaultTestModel
	}
	mappedModel := account.GetMappedModel(requestedModel)
	if strings.TrimSpace(mappedModel) == "" {
		mappedModel = requestedModel
	}
	upstreamModel := kiropkg.MapModel(mappedModel)
	if upstreamModel == "" {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Unsupported Kiro model: %s", requestedModel))
	}

	var accessToken string
	switch account.Type {
	case AccountTypeOAuth:
		if s.kiroTokenProvider == nil {
			return s.sendErrorAndEnd(c, "Kiro token provider not configured")
		}
		token, err := s.kiroTokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to get Kiro access token: %s", err.Error()))
		}
		accessToken = token
	case AccountTypeAPIKey:
		accessToken = firstKiroCredential(account, "kiro_api_key", "kiroApiKey", "api_key")
		if accessToken == "" {
			return s.sendErrorAndEnd(c, "Kiro API key is missing")
		}
	default:
		return s.sendErrorAndEnd(c, fmt.Sprintf("Unsupported Kiro account type: %s", account.Type))
	}

	testPayload, err := createTestPayload(requestedModel)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create Kiro test payload")
	}
	testBody, err := json.Marshal(testPayload)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to encode Kiro test payload")
	}
	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(
		ctx,
		account,
		nil,
		testBody,
		upstreamModel,
		accessToken,
		requestedModel,
		nil,
	)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to build Kiro request: %s", err.Error()))
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{Type: "test_start", Model: requestedModel})

	endpoints := buildKiroEndpoints(account)
	if len(endpoints) == 0 {
		return s.sendErrorAndEnd(c, "No Kiro upstream endpoint is configured")
	}
	endpoint := endpoints[0]
	makeRequest := func(token string) (*http.Response, error) {
		req, reqErr := newKiroJSONRequest(
			ctx,
			endpoint.URL,
			buildResult.Payload,
			token,
			buildKiroAccountKey(account),
			buildKiroMachineID(account),
			endpoint.AmzTarget,
			account,
		)
		if reqErr != nil {
			return nil, reqErr
		}
		return s.httpUpstream.DoWithTLS(
			req,
			kiroProxyURL(account),
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}

	resp, err := makeRequest(accessToken)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Kiro request failed: %s", err.Error()))
	}
	if resp.StatusCode == http.StatusUnauthorized && account.Type == AccountTypeOAuth && s.kiroTokenProvider != nil {
		_ = resp.Body.Close()
		refreshedToken, refreshErr := s.kiroTokenProvider.ForceRefreshAccessToken(ctx, account)
		if refreshErr != nil {
			return s.sendErrorAndEnd(c, fmt.Sprintf("Kiro token refresh failed after 401: %s", refreshErr.Error()))
		}
		resp, err = makeRequest(refreshedToken)
		if err != nil {
			return s.sendErrorAndEnd(c, fmt.Sprintf("Kiro retry request failed: %s", err.Error()))
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		return s.sendErrorAndEnd(c, fmt.Sprintf("Kiro API returned %d: %s", resp.StatusCode, string(body)))
	}

	parseResult, err := kiropkg.ParseNonStreamingEventStreamWithContext(resp.Body, requestedModel, buildResult.Context)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to parse Kiro response: %s", err.Error()))
	}
	for _, block := range gjson.GetBytes(parseResult.ResponseBody, "content").Array() {
		if block.Get("type").String() != "text" {
			continue
		}
		if text := block.Get("text").String(); text != "" {
			s.sendEvent(c, TestEvent{Type: "content", Text: text})
		}
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}
