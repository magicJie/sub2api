//go:build unit

package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestKiroMixedNativeSearchReturns400BeforeTokenLookup(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4.5","stream":%t,"tools":[{"name":"Read","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":"find Go release notes"}]}`, stream))
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
			require.NoError(t, err)
			accepted := 0
			parsed.OnUpstreamAccepted = func() { accepted++ }
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			_, err = (&GatewayService{}).forwardKiroMessages(t.Context(), c, &Account{ID: 1, Platform: PlatformKiro, Type: AccountTypeOAuth}, parsed, time.Now())
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
			require.Zero(t, accepted)
		})
	}
}
