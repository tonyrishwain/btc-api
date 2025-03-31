// Unit tests for Bitcoin node API server.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MockClient is a mock of the Bitcoin RPC client
type MockClient struct {
	mockGetBlockCount func() (int64, error)
	mockGetBlockHash  func(blockHeight int64) (*chainhash.Hash, error)
	mockGetBlock      func(blockHash *chainhash.Hash) (*wire.MsgBlock, error)
}

func (m *MockClient) GetBlockCount() (int64, error) {
	return m.mockGetBlockCount()
}

func (m *MockClient) GetBlockHash(blockHeight int64) (*chainhash.Hash, error) {
	return m.mockGetBlockHash(blockHeight)
}

func (m *MockClient) GetBlock(blockHash *chainhash.Hash) (*wire.MsgBlock, error) {
	return m.mockGetBlock(blockHash)
}

func TestMetricsHandler(t *testing.T) {
	// Reset Prometheus metrics
	bitcoinNodeBlockHeight.Set(12345)
	bitcoinNodeConnectionStatus.Set(1)
	transactionsAboveThresholdTotal.Set(100)
	btcVolumeAboveThreshold.Set(50)
	httpRequestsTotal.WithLabelValues("/chainStatus").Add(5)
	httpRequestsTotal.WithLabelValues("/getTransactionsSummary").Add(3)
	httpRequestDuration.WithLabelValues("/chainStatus").Observe(0.1)
	httpRequestDuration.WithLabelValues("/getTransactionsSummary").Observe(0.2)

	// Create a request to pass to our handler
	req, err := http.NewRequest("GET", "/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a ResponseRecorder to record the response
	rr := httptest.NewRecorder()

	// Use promhttp.Handler() directly as it's used in our main application
	handler := promhttp.Handler()

	// Call the handler
	handler.ServeHTTP(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Check the response body for expected metrics
	body := rr.Body.String()
	expectedMetrics := []string{
		"bitcoin_node_block_height 12345",
		"bitcoin_node_connection_status 1",
		"transactions_above_threshold_total 100",
		"btc_volume_above_threshold 50",
		"http_requests_total{endpoint=\"/chainStatus\"} 5",
		"http_requests_total{endpoint=\"/getTransactionsSummary\"} 3",
		"http_request_duration_seconds_sum{endpoint=\"/chainStatus\"}",
		"http_request_duration_seconds_sum{endpoint=\"/getTransactionsSummary\"}",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("Expected metric not found: %s", metric)
		}
	}
}

func TestChainStatusHandler(t *testing.T) {
	// Create a mock client
	mockClient := &MockClient{
		mockGetBlockCount: func() (int64, error) {
			return 12345, nil
		},
	}

	// Replace the global client with our mock
	originalClient := client
	client = mockClient
	defer func() { client = originalClient }()

	// Create a request to pass to our handler
	req, err := http.NewRequest("GET", "/chainStatus", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a ResponseRecorder to record the response
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(chainStatusHandler)

	// Call the handler
	handler.ServeHTTP(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Check the response body
	expected := map[string]interface{}{
		"chain":             "OK",
		"last_block_height": float64(12345), // JSON numbers are floats
	}
	var got map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &got)
	if err != nil {
		t.Fatal(err)
	}
	if got["chain"] != expected["chain"] || got["last_block_height"] != expected["last_block_height"] {
		t.Errorf("handler returned unexpected body: got %v want %v", got, expected)
	}
}

func TestGetTransactionsSummaryHandler(t *testing.T) {
	// Create a mock client
	mockClient := &MockClient{
		mockGetBlockCount: func() (int64, error) {
			return 12345, nil
		},
		mockGetBlockHash: func(blockHeight int64) (*chainhash.Hash, error) {
			hash, _ := chainhash.NewHashFromStr("000000000000000000024bead8df69990852c202db0e0097c1a12ea637d7e96d")
			return hash, nil
		},
		mockGetBlock: func(blockHash *chainhash.Hash) (*wire.MsgBlock, error) {
			// Create a mock block with a single transaction
			tx := wire.NewMsgTx(wire.TxVersion)
			tx.AddTxOut(wire.NewTxOut(100000000, []byte{})) // 1 BTC output

			// Create empty hashes for the block header
			emptyHash := chainhash.Hash{}

			block := wire.NewMsgBlock(wire.NewBlockHeader(0, &emptyHash, &emptyHash, 0, 0))
			block.AddTransaction(tx)
			return block, nil
		},
	}

	// Replace the global client with our mock
	originalClient := client
	client = mockClient
	defer func() { client = originalClient }()

	// Create a request to pass to our handler
	req, err := http.NewRequest("GET", "/getTransactionsSummary?threshold=0.5", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a ResponseRecorder to record the response
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(getTransactionsSummaryHandler)

	// Call the handler
	handler.ServeHTTP(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Check the response body
	expected := map[string]interface{}{
		"total_transactions": float64(25), // One transaction per block, for 25 blocks
		"total_btc":          float64(25), // 1 BTC per transaction, for 25 blocks
	}
	var got map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &got)
	if err != nil {
		t.Fatal(err)
	}
	if got["total_transactions"] != expected["total_transactions"] || got["total_btc"] != expected["total_btc"] {
		t.Errorf("handler returned unexpected body: got %v want %v", got, expected)
	}
}

func TestSummaryHandler(t *testing.T) {
	// Reset Prometheus metrics
	bitcoinNodeBlockHeight.Set(12345)
	bitcoinNodeConnectionStatus.Set(1)
	transactionsAboveThresholdTotal.Set(100)
	btcVolumeAboveThreshold.Set(50)

	// Create a request to pass to our handler
	req, err := http.NewRequest("GET", "/summary", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a ResponseRecorder to record the response
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(summaryHandler)

	// Call the handler
	handler.ServeHTTP(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Check the response body
	expected := `# Bitcoin Node Status
bitcoin_node_block_height 12345
bitcoin_node_connection_status 1

# Transaction Summary
transactions_above_threshold_total 100
btc_volume_above_threshold 50

`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}
