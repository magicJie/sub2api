package service

import (
	"strconv"
	"strings"
)

// GenerateKiroSessionHash keeps Kiro account affinity stable when OpenAI clients
// resend an expanding conversation without a metadata session ID. This is kept
// separate from the generic scheduler hash because other platforms intentionally
// distinguish each full conversation state.
func (s *GatewayService) GenerateKiroSessionHash(parsed *ParsedRequest) string {
	if parsed == nil {
		return ""
	}
	if parsed.MetadataUserID != "" {
		if uid := ParseMetadataUserID(parsed.MetadataUserID); uid != nil && uid.SessionID != "" {
			return uid.SessionID
		}
	}
	if cacheableContent := s.extractCacheableContent(parsed); cacheableContent != "" {
		return s.hashContent(cacheableContent)
	}

	anchorType, anchor := kiroConversationAnchor(parsed, parsed.Body.Bytes())
	if anchor == "" {
		return s.GenerateSessionHash(parsed)
	}
	var seed strings.Builder
	if parsed.SessionContext != nil {
		_, _ = seed.WriteString(parsed.SessionContext.ClientIP)
		_, _ = seed.WriteString(":")
		_, _ = seed.WriteString(NormalizeSessionUserAgent(parsed.SessionContext.UserAgent))
		_, _ = seed.WriteString(":")
		_, _ = seed.WriteString(strconv.FormatInt(parsed.SessionContext.APIKeyID, 10))
		_, _ = seed.WriteString("|")
	}
	_, _ = seed.WriteString(anchorType)
	_, _ = seed.WriteString(":")
	_, _ = seed.WriteString(anchor)
	return s.hashContent(seed.String())
}

// GenerateSessionHashForPlatform 对 Kiro 平台使用 GenerateKiroSessionHash，其余平台走上游逻辑。
func (s *GatewayService) GenerateSessionHashForPlatform(parsed *ParsedRequest, platform string) string {
	if platform == PlatformKiro {
		return s.GenerateKiroSessionHash(parsed)
	}
	return s.GenerateSessionHash(parsed)
}

// IsKiroOAuthAccount 报告账号是否为 Kiro OAuth 账号（供 handler 层 UMQ 判断）。
func IsKiroOAuthAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformKiro && account.Type == AccountTypeOAuth
}
