package service

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

var (
	errOllamaCloudInvalidJSON     = errors.New("invalid_json")
	errOllamaCloudIncompleteUsage = errors.New("incomplete_usage")
)

// parseOllamaCloudBalance validates the two documented balance variants at the API boundary.
func parseOllamaCloudBalance(body []byte) (*OllamaCloudUsageData, error) {
	type legacyLimit struct {
		RemainingPercent *float64   `json:"remaining_percent"`
		ResetsAt         *time.Time `json:"resets_at"`
	}
	var response struct {
		Included *struct {
			BalanceUSD   *float64 `json:"balance_usd"`
			AllowanceUSD *float64 `json:"allowance_usd"`
			Period       *struct {
				From  time.Time `json:"from"`
				Until time.Time `json:"until"`
			} `json:"period"`
			Session *legacyLimit `json:"session"`
			Weekly  *legacyLimit `json:"weekly"`
		} `json:"included"`
		Purchased *struct {
			BalanceUSD *float64 `json:"balance_usd"`
		} `json:"purchased"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errOllamaCloudInvalidJSON
	}
	if response.Included == nil || response.Purchased == nil || response.Purchased.BalanceUSD == nil {
		return nil, errOllamaCloudIncompleteUsage
	}
	included := response.Included
	total := *response.Purchased.BalanceUSD
	data := &OllamaCloudUsageData{}
	if included.BalanceUSD != nil || included.AllowanceUSD != nil || included.Period != nil {
		if included.BalanceUSD == nil || included.AllowanceUSD == nil || *included.AllowanceUSD < 0 || included.Period == nil ||
			included.Period.From.IsZero() || !included.Period.Until.After(included.Period.From) || included.Session != nil || included.Weekly != nil {
			return nil, errOllamaCloudIncompleteUsage
		}
		total += *included.BalanceUSD
		if *included.AllowanceUSD > 0 {
			reset := included.Period.Until.UTC()
			data.Monthly = &OllamaCloudUsageWindow{
				UsedPercent: math.Max(0, math.Min(100, (1 - *included.BalanceUSD / *included.AllowanceUSD)*100)),
				ResetAt:     &reset,
			}
		}
	} else {
		for index, limit := range []*legacyLimit{included.Session, included.Weekly} {
			if limit == nil || limit.RemainingPercent == nil || *limit.RemainingPercent < 0 || *limit.RemainingPercent > 100 || limit.ResetsAt == nil || limit.ResetsAt.IsZero() {
				return nil, errOllamaCloudIncompleteUsage
			}
			reset := limit.ResetsAt.UTC()
			window := &OllamaCloudUsageWindow{UsedPercent: 100 - *limit.RemainingPercent, ResetAt: &reset}
			if index == 0 {
				data.FiveHour = window
			} else {
				data.SevenDay = window
			}
		}
	}
	// Normalize binary sum noise without rounding tiny positive credits to zero.
	total, err := strconv.ParseFloat(strconv.FormatFloat(total, 'g', 15, 64), 64)
	if err != nil || math.IsInf(total, 0) || math.IsNaN(total) {
		return nil, errOllamaCloudIncompleteUsage
	}
	// Fixed-point output is also consumed by the 429 recovery balance check.
	data.Balance = "$" + strconv.FormatFloat(total, 'f', -1, 64)
	return data, nil
}

type ollamaCloudAPIMetrics struct {
	RequestCount      *int64   `json:"request_count"`
	UsageUSD          *float64 `json:"usage_usd,omitempty"`
	InputTokens       *int64   `json:"input_tokens,omitempty"`
	CachedInputTokens *int64   `json:"cached_input_tokens,omitempty"`
	OutputTokens      *int64   `json:"output_tokens,omitempty"`
}

func (m ollamaCloudAPIMetrics) validated() (OllamaCloudUsageMetrics, bool) {
	if m.RequestCount == nil || *m.RequestCount < 0 || (m.UsageUSD != nil && *m.UsageUSD < 0) {
		return OllamaCloudUsageMetrics{}, false
	}
	for _, count := range []*int64{m.InputTokens, m.CachedInputTokens, m.OutputTokens} {
		if count != nil && *count < 0 {
			return OllamaCloudUsageMetrics{}, false
		}
	}
	return OllamaCloudUsageMetrics{
		RequestCount: *m.RequestCount, UsageUSD: m.UsageUSD, InputTokens: m.InputTokens,
		CachedInputTokens: m.CachedInputTokens, OutputTokens: m.OutputTokens,
	}, true
}

func parseOllamaCloudUsage(body []byte) (*OllamaCloudUsageHistory, error) {
	var response struct {
		Range  string    `json:"range"`
		Scope  string    `json:"scope"`
		From   time.Time `json:"from"`
		Until  time.Time `json:"until"`
		Totals *struct {
			ollamaCloudAPIMetrics
			Groups []struct {
				Model string `json:"model"`
				ollamaCloudAPIMetrics
			} `json:"groups"`
			Remainder *ollamaCloudAPIMetrics `json:"remainder"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errOllamaCloudInvalidJSON
	}
	if response.Range != "7d" || response.Scope != "self" || response.From.IsZero() || !response.Until.After(response.From) || response.Totals == nil {
		return nil, errOllamaCloudIncompleteUsage
	}
	metrics, valid := response.Totals.ollamaCloudAPIMetrics.validated()
	if !valid {
		return nil, errOllamaCloudIncompleteUsage
	}
	history := &OllamaCloudUsageHistory{
		Range: response.Range, Scope: response.Scope, From: response.From.UTC(), Until: response.Until.UTC(),
		Totals: OllamaCloudUsageTotals{
			OllamaCloudUsageMetrics: metrics,
			Groups:                  make([]OllamaCloudUsageGroup, 0, len(response.Totals.Groups)),
		},
	}
	for _, group := range response.Totals.Groups {
		metrics, valid := group.ollamaCloudAPIMetrics.validated()
		if !valid || strings.TrimSpace(group.Model) == "" {
			return nil, errOllamaCloudIncompleteUsage
		}
		history.Totals.Groups = append(history.Totals.Groups, OllamaCloudUsageGroup{Model: group.Model, OllamaCloudUsageMetrics: metrics})
	}
	if response.Totals.Remainder != nil {
		metrics, valid := response.Totals.Remainder.validated()
		if !valid {
			return nil, errOllamaCloudIncompleteUsage
		}
		history.Totals.Remainder = &metrics
	}
	return history, nil
}
