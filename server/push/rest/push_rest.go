package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store/types"
)

var handler Handler

const (
	// Default buffer size for channels
	defaultBufferSize = 1024
	// Default timeout for HTTP requests
	defaultTimeout = 30 * time.Second
	// Default batch size for push requests
	defaultBatchSize = 100
	// Default max retries
	defaultMaxRetries = 3
	// Default retry delay in seconds
	defaultRetryDelay = 5
)

// Handler represents the REST push notification handler.
type Handler struct {
	input   chan *push.Receipt
	channel chan *push.ChannelReq
	stop    chan bool
	config  *configType
	client  *http.Client
}

// configType defines configuration parameters for the REST push adapter.
type configType struct {
	// Enable or disable the adapter
	Enabled bool `json:"enabled"`
	// REST API endpoint URL
	Endpoint string `json:"endpoint"`
	// HTTP method (POST, PUT, etc.)
	Method string `json:"method"`
	// Custom headers to include in requests
	Headers map[string]string `json:"headers"`
	// Bearer token for authentication
	AuthToken string `json:"auth_token"`
	// Request timeout in seconds
	Timeout int `json:"timeout"`
	// Number of notifications per batch (unused for now)
	BatchSize int `json:"batch_size"`
	// Channel buffer size
	BufferSize int `json:"buffer_size"`
	// Enable gzip compression (unused for now)
	Compress bool `json:"compress"`
	// Number of retry attempts
	MaxRetries int `json:"max_retries"`
	// Delay between retries in seconds
	RetryDelay int `json:"retry_delay"`
	// Whether to perform health check on startup
	HealthCheckEnabled bool `json:"health_check_enabled"`
	// Health check endpoint URL
	HealthURL string `json:"health_url"`
}

// restRecipient represents a push notification recipient in REST format.
type restRecipient struct {
	// Count of user's connections that were live when the packet was dispatched
	Delivered int `json:"delivered"`
	// List of user's devices that the packet was delivered to
	Devices []string `json:"devices,omitempty"`
	// Unread count to include in the push
	Unread int `json:"unread"`
}

// restReceipt represents a push notification receipt in REST format.
type restReceipt struct {
	// Recipients mapped by user ID
	To map[string]restRecipient `json:"to"`
	// Channel for group notifications
	Channel string `json:"channel,omitempty"`
	// Notification payload
	Payload push.Payload `json:"payload"`
}

// Init initializes the push handler.
func (Handler) Init(jsonconf json.RawMessage) (bool, error) {
	var config configType
	if err := json.Unmarshal(jsonconf, &config); err != nil {
		return false, errors.New("rest push: failed to parse config: " + err.Error())
	}

	if !config.Enabled {
		return false, nil
	}

	if config.Endpoint == "" {
		return false, errors.New("rest push: endpoint URL is required")
	}

	// Set defaults
	if config.Method == "" {
		config.Method = "POST"
	}
	if config.Timeout <= 0 {
		config.Timeout = int(defaultTimeout.Seconds())
	}
	if config.BatchSize <= 0 {
		config.BatchSize = defaultBatchSize
	}
	if config.BufferSize <= 0 {
		config.BufferSize = defaultBufferSize
	}
	if config.MaxRetries < 0 {
		config.MaxRetries = defaultMaxRetries
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = defaultRetryDelay
	}

	// Initialize handler
	handler.input = make(chan *push.Receipt, config.BufferSize)
	handler.channel = make(chan *push.ChannelReq, config.BufferSize)
	handler.stop = make(chan bool, 1)
	handler.config = &config
	handler.client = &http.Client{
		Timeout: time.Duration(config.Timeout) * time.Second,
	}

	// Perform health check on startup if enabled
	if config.HealthCheckEnabled && config.HealthURL != "" {
		go handler.testHealthCheck()
	}

	// Start worker goroutine
	go handler.run()

	return true, nil
}

// run is the main worker goroutine that processes incoming requests.
func (h *Handler) run() {
	for {
		select {
		case rcpt := <-h.input:
			go h.handlePushNotification(rcpt)
		case req := <-h.channel:
			// Channel subscription requests are logged but not forwarded
			logs.Info.Printf("REST push: channel request - User: %s, Channel: %s, Unsub: %t",
				req.Uid.UserId(), req.Channel, req.Unsub)
		case <-h.stop:
			return
		}
	}
}

// testHealthCheck performs a health check against the configured endpoint.
func (h *Handler) testHealthCheck() {
	if h.config.HealthURL == "" {
		logs.Warn.Println("REST push: health check enabled but no health URL configured")
		return
	}

	resp, err := h.client.Get(h.config.HealthURL)
	if err != nil {
		logs.Warn.Printf("REST push: health check failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logs.Info.Printf("REST push: health check successful - HTTP %d", resp.StatusCode)
	} else {
		logs.Warn.Printf("REST push: health check failed - HTTP %d", resp.StatusCode)
	}
}

// handlePushNotification processes a single push notification.
func (h *Handler) handlePushNotification(rcpt *push.Receipt) {
	if rcpt == nil {
		logs.Warn.Println("REST push: received nil receipt")
		return
	}

	// Convert to REST format
	restRcpt := h.convertToRestFormat(rcpt)

	// Marshal to JSON
	jsonData, err := json.Marshal(restRcpt)
	if err != nil {
		logs.Warn.Printf("REST push: failed to marshal receipt: %v", err)
		return
	}

	logs.Info.Printf("REST push: sending notification to %d recipients", len(restRcpt.To))

	// Send with retry logic
	h.sendWithRetry(jsonData)
}

// convertToRestFormat converts a push.Receipt to REST format with user ID conversion.
func (h *Handler) convertToRestFormat(rcpt *push.Receipt) *restReceipt {
	restRcpt := &restReceipt{
		To:      make(map[string]restRecipient),
		Channel: rcpt.Channel,
		Payload: rcpt.Payload,
	}

	// Convert numeric UIDs to user ID format and build recipients map
	for uid, recipient := range rcpt.To {
		userID := uid.UserId()
		restRcpt.To[userID] = restRecipient{
			Delivered: recipient.Delivered,
			Unread:    recipient.Unread,
			Devices:   recipient.Devices,
		}
	}

	// Convert 'from' field if it's a numeric UID
	if fromUid := types.ParseUid(rcpt.Payload.From); !fromUid.IsZero() {
		restRcpt.Payload.From = fromUid.UserId()
	}

	return restRcpt
}

// sendWithRetry sends the JSON payload with exponential backoff retry.
func (h *Handler) sendWithRetry(jsonData []byte) {
	var lastErr error
	for attempt := 0; attempt <= h.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(h.config.RetryDelay*attempt) * time.Second
			logs.Info.Printf("REST push: retry attempt %d/%d after %v", attempt, h.config.MaxRetries, delay)
			time.Sleep(delay)
		}

		err := h.sendHTTPRequest(jsonData)
		if err == nil {
			if attempt > 0 {
				logs.Info.Printf("REST push: notification sent successfully after %d retries", attempt)
			} else {
				logs.Info.Println("REST push: notification sent successfully")
			}
			return
		}

		lastErr = err
		logs.Warn.Printf("REST push: attempt %d failed: %v", attempt+1, err)
	}

	logs.Err.Printf("REST push: failed after %d attempts: %v", h.config.MaxRetries+1, lastErr)
}

// sendHTTPRequest sends the actual HTTP request.
func (h *Handler) sendHTTPRequest(jsonData []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(h.config.Timeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, h.config.Method, h.config.Endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return err
	}

	// Add custom headers
	for key, value := range h.config.Headers {
		req.Header.Set(key, value)
	}

	// Add authentication if provided
	if h.config.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.config.AuthToken)
	}

	// Execute request
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return errors.New("HTTP " + resp.Status + ": " + string(body))
	}

	return nil
}

// IsReady checks if the push handler has been initialized.
func (Handler) IsReady() bool {
	return handler.input != nil
}

func (Handler) Push() chan<- *push.Receipt {
	go handler.testHealthCheck()
	return handler.input
}

func (Handler) Channel() chan<- *push.ChannelReq {
	return handler.channel
}

// Stop shuts down the handler
func (Handler) Stop() {
	handler.stop <- true
}

func init() {
	push.Register("rest", &handler)
}
