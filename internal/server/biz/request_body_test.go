package biz

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
)

func TestRequestBodyForPersistence(t *testing.T) {
	t.Run("valid wire JSON is authoritative", func(t *testing.T) {
		body, err := requestBodyForPersistence(httpclient.Request{
			Body:     []byte(`{"service_tier":"priority"}`),
			JSONBody: []byte(`{"service_tier":"default"}`),
		})
		require.NoError(t, err)
		require.JSONEq(t, `{"service_tier":"priority"}`, string(body))
	})

	t.Run("non JSON wire body uses logging representation", func(t *testing.T) {
		body, err := requestBodyForPersistence(httpclient.Request{
			Body:     []byte("multipart body"),
			JSONBody: []byte(`{"service_tier":"priority"}`),
		})
		require.NoError(t, err)
		require.JSONEq(t, `{"service_tier":"priority"}`, string(body))
	})
}
