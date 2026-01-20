//go:build e2e

// Package e2e — E2E тесты Saga flow.
// Запуск: go test -tags=e2e -v ./tests/e2e/...
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	gatewayURL    = "http://localhost:8080"
	healthTimeout = 5 * time.Second
	sagaTimeout   = 15 * time.Second
	pollInterval  = 500 * time.Millisecond
)

// DTO — только используемые поля
type (
	registerReq  struct{ Email, Password, Name string }
	registerResp struct {
		UserID string `json:"user_id"`
	}
	loginReq  struct{ Email, Password string }
	loginResp struct {
		AccessToken string `json:"access_token"`
	}
	money struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}
	orderItem struct {
		ProductID   string `json:"product_id"`
		ProductName string `json:"product_name"`
		Quantity    int32  `json:"quantity"`
		UnitPrice   money  `json:"unit_price"`
	}
	createOrderReq struct {
		Items          []orderItem
		IdempotencyKey string `json:"idempotency_key"`
	}
	createOrderResp struct {
		OrderID string `json:"order_id"`
	}
	orderResp struct {
		Order struct {
			Status        string  `json:"status"`
			PaymentID     *string `json:"payment_id,omitempty"`
			FailureReason *string `json:"failure_reason,omitempty"`
		} `json:"order"`
	}
)

func TestMain(m *testing.M) {
	if !waitForGateway(healthTimeout) {
		fmt.Printf("⚠️  Gateway %s недоступен, E2E тесты пропущены\n", gatewayURL)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func waitForGateway(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		if resp, err := client.Get(gatewayURL + "/health"); err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// testClient — HTTP клиент с хелперами
type testClient struct{ http *http.Client }

func newTestClient() *testClient {
	return &testClient{http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *testClient) register(t *testing.T, email, password, name string) {
	t.Helper()
	body, _ := json.Marshal(registerReq{email, password, name})
	resp, err := c.http.Post(gatewayURL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return // Пользователь уже существует — OK
	}
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

func (c *testClient) login(t *testing.T, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(loginReq{email, password})
	resp, err := c.http.Post(gatewayURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(respBody))
	var result loginResp
	require.NoError(t, json.Unmarshal(respBody, &result))
	return result.AccessToken
}

func (c *testClient) createOrder(t *testing.T, token string, items []orderItem) string {
	t.Helper()
	body, _ := json.Marshal(createOrderReq{Items: items, IdempotencyKey: uuid.New().String()})
	req, _ := http.NewRequest(http.MethodPost, gatewayURL+"/api/v1/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(respBody))
	var result createOrderResp
	require.NoError(t, json.Unmarshal(respBody, &result))
	return result.OrderID
}

func (c *testClient) getOrder(t *testing.T, token, orderID string) *orderResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, gatewayURL+"/api/v1/orders/"+orderID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(respBody))
	var result orderResp
	require.NoError(t, json.Unmarshal(respBody, &result))
	return &result
}

func (c *testClient) waitForStatus(t *testing.T, token, orderID, expected string) *orderResp {
	t.Helper()
	deadline := time.Now().Add(sagaTimeout)
	for time.Now().Before(deadline) {
		order := c.getOrder(t, token, orderID)
		if order.Order.Status == expected || order.Order.Status == "FAILED" || order.Order.Status == "CANCELLED" {
			return order
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("Таймаут: заказ %s не достиг статуса %s", orderID, expected)
	return nil
}

// TestSagaFlow — полный flow: Register → Login → CreateOrder → Saga → Final Status
func TestSagaFlow(t *testing.T) {
	tests := []struct {
		name          string
		amount        int64
		expectStatus  string
		expectPayment bool
	}{
		{"success", 10000, "CONFIRMED", true},
		{"payment_failed", 6660, "FAILED", false}, // Кратно 666 → отказ
	}

	client := newTestClient()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			email := fmt.Sprintf("e2e-%s-%s@test.local", tt.name, uuid.New().String()[:8])
			items := []orderItem{{
				ProductID:   uuid.New().String(),
				ProductName: "Тестовый товар",
				Quantity:    1,
				UnitPrice:   money{Amount: tt.amount, Currency: "RUB"},
			}}

			// Act
			client.register(t, email, "TestPassword123!", "E2E User")
			token := client.login(t, email, "TestPassword123!")
			orderID := client.createOrder(t, token, items)
			order := client.waitForStatus(t, token, orderID, tt.expectStatus)

			// Assert
			assert.Equal(t, tt.expectStatus, order.Order.Status)
			if tt.expectPayment {
				assert.NotNil(t, order.Order.PaymentID)
			} else {
				assert.NotNil(t, order.Order.FailureReason)
			}
		})
	}
}
