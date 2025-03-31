// Bitcoin node API server with Prometheus integration.
package main

/*
High-Level Overview:
This application is a Bitcoin node API server that connects to a Bitcoin node,
collects various metrics, and exposes them through HTTP endpoints and Prometheus.

Key features and functionality:
1. Bitcoin Node Connection:
   - Connects to a Bitcoin node using RPC.
   - Retrieves current block height and monitors connection status.

2. Transaction Analysis:
   - Analyzes transactions in the last 25 blocks.
   - Calculates total number and volume of transactions above a specified threshold.

3. Metrics Collection:
   - Collects HTTP request metrics (total requests and duration).
   - Tracks Bitcoin node status (block height and connection status).
   - Monitors transaction metrics (count and volume above threshold).

4. HTTP Endpoints:
   - /chainStatus: Returns current chain status (last block height).
   - /getTransactionsSummary: Provides a summary of transactions above a threshold.
   - /metrics: Exposes all Prometheus metrics.
   - /summary: Offers a custom-formatted summary of selected metrics.

5. Prometheus Integration:
   - Registers and updates Prometheus metrics.
   - Exposes metrics for scraping via the /metrics endpoint.

6. Periodic Updates:
   - Runs a background goroutine to update metrics every minute.

7. Error Handling and Logging:
   - Implements retry mechanism for Bitcoin node connection.
   - Logs errors and important events for monitoring and debugging.
*/

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/wire"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

// BitcoinClient interface
type BitcoinClient interface {
	GetBlockCount() (int64, error)
	GetBlockHash(blockHeight int64) (*chainhash.Hash, error)
	GetBlock(blockHash *chainhash.Hash) (*wire.MsgBlock, error)
}

// Global variables
var (
	client BitcoinClient
	//client *rpcclient.Client
	mu sync.Mutex

	// Prometheus metrics
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"endpoint"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests in seconds",
			Buckets: []float64{0.01, 0.1, 1},
		},
		[]string{"endpoint"},
	)
	bitcoinNodeBlockHeight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_node_block_height",
		Help: "Current block height of the Bitcoin node",
	})
	bitcoinNodeConnectionStatus = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bitcoin_node_connection_status",
		Help: "Connection status to the Bitcoin node (1 = connected, 0 = disconnected)",
	})
	transactionsAboveThresholdTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "transactions_above_threshold_total",
		Help: "Total number of transactions above the threshold in the last 25 blocks",
	})
	btcVolumeAboveThreshold = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "btc_volume_above_threshold",
		Help: "Total BTC volume of transactions above the threshold in the last 25 blocks",
	})
)

// init registers all Prometheus metrics.
func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(bitcoinNodeBlockHeight)
	prometheus.MustRegister(bitcoinNodeConnectionStatus)
	prometheus.MustRegister(transactionsAboveThresholdTotal)
	prometheus.MustRegister(btcVolumeAboveThreshold)
}

// initClient used to create connection to the node
func initClient() error {
	// Configure the connection
	connCfg := &rpcclient.ConnConfig{
		Host:         os.Getenv("BITCOIN_RPC_HOST"),
		User:         os.Getenv("BITCOIN_RPC_USER"),
		Pass:         os.Getenv("BITCOIN_RPC_PASSWORD"),
		HTTPPostMode: true,
		DisableTLS:   true,
	}

	// Attempt to connect with retries
	var err error
	var rpcClient *rpcclient.Client
	for retries := 0; retries < 5; retries++ {
		rpcClient, err = rpcclient.New(connCfg, nil)
		if err == nil {
			break
		}
		log.Printf("Failed to create RPC client (attempt %d/5): %v", retries+1, err)
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		return err
	}
	client = rpcClient
	return nil
}

// main is the entry point of the application.
func main() {
	// Initialize Bitcoin node client connection
	if err := initClient(); err != nil {
		log.Fatalf("Failed to create RPC client after 5 attempts: %v", err)
	}
	defer client.(*rpcclient.Client).Shutdown()

	// Verify connection by getting the current block count
	blockCount, err := client.GetBlockCount()
	if err != nil {
		log.Printf("Failed to get block count: %v", err)
		log.Println("Continuing execution. Some functionality may be limited.")
	} else {
		log.Printf("Successfully connected to Bitcoin node. Current block count: %d", blockCount)
	}

	// Start a goroutine to update metrics periodically
	go func() {
		for {
			updateMetrics()
			time.Sleep(1 * time.Minute)
		}
	}()

	// Set up HTTP handlers
	http.HandleFunc("/chainStatus", chainStatusHandler)
	http.HandleFunc("/getTransactionsSummary", getTransactionsSummaryHandler)
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/summary", summaryHandler)

	// Start the HTTP server
	log.Println("Starting server on 0.0.0.0:8080")
	log.Fatal(http.ListenAndServe("0.0.0.0:8080", nil))
}

// updateMetrics refreshes all Prometheus metrics.
func updateMetrics() {
	blockCount, err := client.GetBlockCount()
	if err == nil {
		bitcoinNodeBlockHeight.Set(float64(blockCount))
		bitcoinNodeConnectionStatus.Set(1)
	} else {
		bitcoinNodeConnectionStatus.Set(0)
	}

	totalTx, totalBTC := getTransactionsSummary(0.0)
	transactionsAboveThresholdTotal.Set(float64(totalTx))
	btcVolumeAboveThreshold.Set(totalBTC)
}

// chainStatusHandler responds with the current chain status.
func chainStatusHandler(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(httpRequestDuration.WithLabelValues("/chainStatus"))
	defer timer.ObserveDuration()
	httpRequestsTotal.WithLabelValues("/chainStatus").Inc()

	blockCount, err := client.GetBlockCount()
	if err != nil {
		log.Printf("Error getting block count: %v", err)
		http.Error(w, fmt.Sprintf("Error getting block count: %v", err), http.StatusInternalServerError)
		return
	}

	response := struct {
		Chain           string `json:"chain"`
		LastBlockHeight int64  `json:"last_block_height"`
	}{
		Chain:           "OK",
		LastBlockHeight: blockCount,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// getTransactionsSummary calculates the total number and volume of transactions above a given threshold in the last 25 blocks.
func getTransactionsSummary(threshold float64) (int, float64) {
	mu.Lock()
	defer mu.Unlock()

	blockCount, err := client.GetBlockCount()
	if err != nil {
		log.Printf("Error getting block count: %v", err)
		return 0, 0
	}

	var totalTx int
	var totalBTC float64

	// Iterate through the last 25 blocks
	for i := blockCount; i > blockCount-25 && i >= 0; i-- {
		blockHash, err := client.GetBlockHash(i)
		if err != nil {
			log.Printf("Error getting block hash for height %d: %v", i, err)
			continue
		}

		block, err := client.GetBlock(blockHash)
		if err != nil {
			log.Printf("Error getting block for hash %s: %v", blockHash, err)
			continue
		}

		// Process each transaction in the block
		for _, tx := range block.Transactions {
			var txValue float64
			for _, out := range tx.TxOut {
				value := float64(out.Value) / 1e8 // Convert satoshis to BTC
				if value > threshold {
					txValue += value
				}
			}
			if txValue > 0 {
				totalTx++
				totalBTC += txValue
			}
		}
	}

	return totalTx, totalBTC
}

// getTransactionsSummaryHandler responds with a summary of transactions above a specified threshold.
func getTransactionsSummaryHandler(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(httpRequestDuration.WithLabelValues("/getTransactionsSummary"))
	defer timer.ObserveDuration()
	httpRequestsTotal.WithLabelValues("/getTransactionsSummary").Inc()

	threshold, _ := strconv.ParseFloat(r.URL.Query().Get("threshold"), 64)

	totalTx, totalBTC := getTransactionsSummary(threshold)

	response := struct {
		TotalTransactions int     `json:"total_transactions"`
		TotalBTC          float64 `json:"total_btc"`
	}{
		TotalTransactions: totalTx,
		TotalBTC:          totalBTC,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// summaryHandler provides a custom summary of selected metrics.
func summaryHandler(w http.ResponseWriter, r *http.Request) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		bitcoinNodeBlockHeight,
		bitcoinNodeConnectionStatus,
		transactionsAboveThresholdTotal,
		btcVolumeAboveThreshold,
	)

	metrics, err := registry.Gather()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error gathering metrics: %v", err), http.StatusInternalServerError)
		return
	}

	formattedMetrics := formatMetrics(metrics)

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(formattedMetrics))
}

// formatMetrics organizes and formats the metrics into a human-readable string.
func formatMetrics(metrics []*dto.MetricFamily) string {
	var buffer bytes.Buffer

	sections := []struct {
		name    string
		metrics []string
	}{
		{"Bitcoin Node Status", []string{"bitcoin_node_block_height", "bitcoin_node_connection_status"}},
		{"Transaction Summary", []string{"transactions_above_threshold_total", "btc_volume_above_threshold"}},
	}

	for _, section := range sections {
		fmt.Fprintf(&buffer, "# %s\n", section.name)
		for _, metricName := range section.metrics {
			for _, mf := range metrics {
				if *mf.Name == metricName {
					writeMetricFamily(&buffer, mf)
				}
			}
		}
		buffer.WriteString("\n")
	}

	return buffer.String()
}

// writeMetricFamily writes a single metric family to the buffer.
func writeMetricFamily(buffer *bytes.Buffer, mf *dto.MetricFamily) {
	for _, m := range mf.Metric {
		switch *mf.Type {
		case dto.MetricType_GAUGE:
			writeGauge(buffer, mf.Name, m)
		}
	}
}

// writeGauge writes a single gauge metric to the buffer.
func writeGauge(buffer *bytes.Buffer, name *string, m *dto.Metric) {
	fmt.Fprintf(buffer, "%s%s %v\n", *name, labelsToString(m.Label), *m.Gauge.Value)
}

// labelsToString converts metric labels to a string representation.
func labelsToString(labels []*dto.LabelPair) string {
	if len(labels) == 0 {
		return ""
	}

	var buffer bytes.Buffer
	buffer.WriteString("{")
	for i, label := range labels {
		if i > 0 {
			buffer.WriteString(",")
		}
		buffer.WriteString(*label.Name)
		buffer.WriteString("=\"")
		buffer.WriteString(*label.Value)
		buffer.WriteString("\"")
	}
	buffer.WriteString("}")
	return buffer.String()
}
