package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/metoro-io/mcp-golang/transport"
	"github.com/sirupsen/logrus"
)

// Add a map to store request metadata
type requestMetadata struct {
	clientId   string
	originalId interface{}
}

// Add at the top with other type definitions
type contextKey string

const clientIDKey contextKey = "clientId"

type WebSocketTransport struct {
	server               *gin.Engine
	httpServer           *http.Server
	messageHandler       func(ctx context.Context, message *transport.BaseJsonRpcMessage)
	connectionHandler    func(clientId string)
	disconnectionHandler func(clientId string)
	closeHandler         func()
	errorHandler         func(error)
	mu                   sync.RWMutex
	responseMap          map[string]chan *transport.BaseJsonRpcMessage
	clients              map[string]*websocket.Conn
	// Track request metadata (client ID and original request ID)
	requestMeta map[interface{}]requestMetadata
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all connections
	},
}

// generateUUID generates a simple unique ID
func generateUUID() string {
	return uuid.New().String()
}

// handleIncomingMessage processes an incoming WebSocket message and returns a response if applicable
func (t *WebSocketTransport) handleIncomingMessage(ctx context.Context, clientId string, message []byte) (*transport.BaseJsonRpcMessage, error) {
	var baseMessage map[string]interface{}
	if err := json.Unmarshal(message, &baseMessage); err != nil {
		return nil, fmt.Errorf("failed to parse message: %v", err)
	}

	// Process the message based on its type
	var deserialized bool
	var messageObj *transport.BaseJsonRpcMessage
	var responseKey string
	var originalId *transport.RequestId

	// Try to unmarshal as a request
	var request transport.BaseJSONRPCRequest
	if err := json.Unmarshal(message, &request); err == nil {
		deserialized = true

		// Keep original ID for later
		id := request.Id
		originalId = &id

		// Associate this request ID with the client ID for later response routing
		t.mu.Lock()
		t.requestMeta[request.Id] = requestMetadata{
			clientId:   clientId,
			originalId: request.Id,
		}
		t.mu.Unlock()

		// Generate a response key - this is where we'll store the response channel
		responseKey = fmt.Sprintf("%s:%v", clientId, request.Id)

		// Keep the original numeric ID
		request.Id = transport.RequestId(request.Id)

		t.mu.Lock()
		// Create a buffered channel to prevent deadlocks if response handler is called before we wait
		t.responseMap[responseKey] = make(chan *transport.BaseJsonRpcMessage, 1)
		t.mu.Unlock()

		messageObj = transport.NewBaseMessageRequest(&request)
	}

	// Try as a notification
	if !deserialized {
		var notification transport.BaseJSONRPCNotification
		if err := json.Unmarshal(message, &notification); err == nil {
			deserialized = true
			messageObj = transport.NewBaseMessageNotification(&notification)
		}
	}

	// Try as a response
	if !deserialized {
		var response transport.BaseJSONRPCResponse
		if err := json.Unmarshal(message, &response); err == nil {
			deserialized = true
			messageObj = transport.NewBaseMessageResponse(&response)
		}
	}

	// Try as an error
	if !deserialized {
		var errorResponse transport.BaseJSONRPCError
		if err := json.Unmarshal(message, &errorResponse); err == nil {
			deserialized = true
			messageObj = transport.NewBaseMessageError(&errorResponse)
		}
	}

	if !deserialized || messageObj == nil {
		return nil, fmt.Errorf("unknown message format")
	}

	// Invoke the message handler if set
	t.mu.RLock()
	handler := t.messageHandler
	t.mu.RUnlock()

	if handler != nil {
		// Create a modified context that includes the client ID
		// This allows the message handler to include it when creating responses
		ctxWithClientID := context.WithValue(ctx, clientIDKey, clientId)
		handler(ctxWithClientID, messageObj)
	}

	// Only wait for a response if this is a request (has an ID and expects a response)
	if originalId != nil {
		// Create a context with timeout
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		t.mu.RLock()
		responseChan, ok := t.responseMap[responseKey]
		t.mu.RUnlock()

		if !ok {
			// Clean up request metadata if the response channel doesn't exist
			t.mu.Lock()
			delete(t.requestMeta, *originalId)
			t.mu.Unlock()
			return nil, fmt.Errorf("response channel not found")
		}

		// Add a timeout to prevent blocking forever
		select {
		case response := <-responseChan:
			// Mark that this response has been handled and clean up resources
			t.mu.Lock()
			delete(t.responseMap, responseKey)
			delete(t.requestMeta, *originalId)
			t.mu.Unlock()

			// Restore original ID if needed
			if response.JsonRpcResponse != nil {
				response.JsonRpcResponse.Id = *originalId
			}

			return response, nil
		case <-timeoutCtx.Done():
			// We're timing out - mark this request as timed out and clean up resources
			t.mu.Lock()
			delete(t.responseMap, responseKey)
			delete(t.requestMeta, *originalId)
			t.mu.Unlock()

			// Simply return a timeout error rather than creating a complex response
			// The router handler will handle this by sending an appropriate error to the client
			return nil, fmt.Errorf("request timed out waiting for response")
		}
	}

	// For notifications and other non-request messages, don't expect a response
	return nil, nil
}

// Function to extract client ID and original ID from a formatted message ID
func (t *WebSocketTransport) parseMessageId(formattedId string) (clientId string, originalId string, ok bool) {
	parts := strings.SplitN(formattedId, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func NewWebSocketTransport(server *gin.Engine) *WebSocketTransport {
	t := &WebSocketTransport{
		server:      server,
		responseMap: make(map[string]chan *transport.BaseJsonRpcMessage),
		clients:     make(map[string]*websocket.Conn),
		requestMeta: make(map[interface{}]requestMetadata),
	}

	router := server.Group("/")
	router.GET("/", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Generate a unique client ID
		clientId := generateUUID()

		// Store the client connection
		t.mu.Lock()
		t.clients[clientId] = conn
		t.mu.Unlock()

		// Set up ping/pong to detect disconnected clients
		conn.SetPingHandler(func(data string) error {
			return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second*5))
		})

		conn.SetPongHandler(func(data string) error {
			return nil
		})

		// Start a goroutine to periodically ping the client
		go func() {
			ticker := time.NewTicker(time.Second * 30)
			defer ticker.Stop()

			for range ticker.C {
				t.mu.RLock()
				_, exists := t.clients[clientId]
				t.mu.RUnlock()

				if !exists {
					return // Client has been removed
				}

				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second*5)); err != nil {
					logrus.Debugf("Failed to ping client %s: %v\n", clientId, err)

					// The client is probably disconnected, clean up
					t.mu.Lock()
					if currentConn, ok := t.clients[clientId]; ok && currentConn == conn {
						delete(t.clients, clientId)
						t.mu.Unlock()

						// Close all associated response channels
						t.cleanupClientChannels(clientId)

						if t.disconnectionHandler != nil {
							t.disconnectionHandler(clientId)
						}
					} else {
						t.mu.Unlock()
					}

					return
				}
			}
		}()

		// Notify about the new connection
		if t.connectionHandler != nil {
			t.connectionHandler(clientId)
		}

		logrus.Infof("Client connected %s", clientId)

		// Handle client disconnection
		defer func() {
			conn.Close()
			t.mu.Lock()
			delete(t.clients, clientId)
			t.mu.Unlock()

			// Clean up all response channels for this client
			t.cleanupClientChannels(clientId)

			if t.disconnectionHandler != nil {
				t.disconnectionHandler(clientId)
			}
		}()

		// Handle incoming messages
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				if t.errorHandler != nil {
					t.errorHandler(err)
				}
				break
			}

			if messageType != websocket.TextMessage {
				continue
			}

			ctx := context.Background()

			// Call handleIncomingMessage and handle both return values
			response, err := t.handleIncomingMessage(ctx, clientId, message)
			if err != nil {
				// Create an error response based on the incoming message
				var errorId transport.RequestId = 0

				// Try to extract the ID from the original message if possible
				var temp map[string]interface{}
				if jsonErr := json.Unmarshal(message, &temp); jsonErr == nil {
					if id, ok := temp["id"]; ok {
						// Convert the ID to our transport.RequestId type
						switch v := id.(type) {
						case float64:
							errorId = transport.RequestId(v)
						case int:
							errorId = transport.RequestId(v)
						case string:
							if intId, convErr := strconv.ParseInt(v, 10, 64); convErr == nil {
								errorId = transport.RequestId(intId)
							}
						}
					}
				}

				// Create an error response
				errorData, jsonErr := json.Marshal(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      errorId,
					"error": map[string]interface{}{
						"code":    -32000,
						"message": err.Error(),
					},
				})

				if jsonErr == nil {
					if writeErr := conn.WriteMessage(websocket.TextMessage, errorData); writeErr != nil && t.errorHandler != nil {
						t.errorHandler(fmt.Errorf("failed to send error response: %v", writeErr))
					}
				}

				if t.errorHandler != nil {
					t.errorHandler(err)
				}
				continue
			}

			// If we got a response, send it back to the client
			if response != nil {
				responseBytes, err := json.Marshal(response)
				if err != nil {
					if t.errorHandler != nil {
						t.errorHandler(fmt.Errorf("failed to marshal response: %v", err))
					}
					continue
				}

				if err := conn.WriteMessage(websocket.TextMessage, responseBytes); err != nil {
					if t.errorHandler != nil {
						t.errorHandler(fmt.Errorf("failed to send response: %v", err))
					}
				}
			}
		}
	})

	return t
}

func (t *WebSocketTransport) Start(ctx context.Context) error {
	// API server is already started by gin
	return nil
}

func (t *WebSocketTransport) Send(ctx context.Context, message *transport.BaseJsonRpcMessage) error {
	// For simplicity, extract the ID from the message based on type
	var idVal interface{}
	var clientId string

	switch {
	case message.JsonRpcRequest != nil:
		idVal = message.JsonRpcRequest.Id
	case message.JsonRpcResponse != nil:
		idVal = message.JsonRpcResponse.Id

		// Look up the client ID from the request metadata
		t.mu.RLock()
		if meta, found := t.requestMeta[idVal]; found {
			clientId = meta.clientId
		}
		t.mu.RUnlock()
	case message.JsonRpcError != nil:
		idVal = message.JsonRpcError.Id

		// Look up the client ID from the request metadata
		t.mu.RLock()
		if meta, found := t.requestMeta[idVal]; found {
			clientId = meta.clientId
		}
		t.mu.RUnlock()
	default:
		// Notification has no ID
		return t.Broadcast(ctx, message)
	}

	// Format the ID string for logging
	idStr := fmt.Sprintf("%v", idVal)

	// Try to get client ID if it's not already set
	if clientId == "" {
		// Try to parse from the ID string in case it contains client ID
		var parsedId string
		var ok bool

		clientId, parsedId, ok = t.parseMessageId(idStr)
		if ok && clientId != "" {
			// If we parsed a client ID from the ID string, use that
			idStr = parsedId
		} else {
			// If we didn't get a properly formatted ID, check the context
			if ctx.Value(clientIDKey) != nil {
				if ctxClientId, ok := ctx.Value(clientIDKey).(string); ok {
					clientId = ctxClientId
					// Keep idStr as is
				}
			}
		}
	}

	// If we still don't have a client ID, broadcast
	if clientId == "" {
		return t.Broadcast(ctx, message)
	}

	// The response key is always in the format clientId:messageId
	// This is the key used to find the response channel
	responseKey := fmt.Sprintf("%s:%s", clientId, idStr)

	t.mu.RLock()
	responseChan, chanExists := t.responseMap[responseKey]
	client, clientExists := t.clients[clientId]
	t.mu.RUnlock()

	// First check if this is a response to a pending request that hasn't timed out
	if chanExists && responseChan != nil {
		// This is a response to a pending request, send it through the channel
		select {
		case responseChan <- message:
			return nil
		default:
			// If the channel is full or gone (timed out already), fall through to direct client send
			logrus.Debugf("Channel full or unavailable for key: %s - sending directly to client\n", responseKey)
		}
	} else {
		logrus.Debugf("No response channel found for key: %s - sending directly to client\n", responseKey)
	}

	// Either the request timed out or this is a new message - send directly to the client
	if clientExists && client != nil {
		// Direct client send
		messageBytes, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("failed to marshal message: %v", err)
		}

		if err := client.WriteMessage(websocket.TextMessage, messageBytes); err != nil {
			return fmt.Errorf("failed to send message to client: %v", err)
		}
		return nil
	}

	return fmt.Errorf("client not found: %s", clientId)
}

func (t *WebSocketTransport) Broadcast(ctx context.Context, message *transport.BaseJsonRpcMessage) error {
	messageBytes, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var errors []error
	for clientId, conn := range t.clients {
		if conn != nil {
			if err := conn.WriteMessage(websocket.TextMessage, messageBytes); err != nil {
				errors = append(errors, fmt.Errorf("failed to send message to client %s: %v", clientId, err))
			}
		}
	}

	if len(errors) > 0 {
		// Return a combined error message
		errorMsgs := make([]string, len(errors))
		for i, err := range errors {
			errorMsgs[i] = err.Error()
		}
		return fmt.Errorf("broadcast errors: %s", strings.Join(errorMsgs, "; "))
	}

	return nil
}

func (t *WebSocketTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Close all client connections
	for clientId, conn := range t.clients {
		if conn != nil {
			conn.Close()
		}
		delete(t.clients, clientId)

		if t.disconnectionHandler != nil {
			t.disconnectionHandler(clientId)
		}
	}

	// Close all response channels
	for _, responseChannel := range t.responseMap {
		close(responseChannel)
	}
	t.responseMap = make(map[string]chan *transport.BaseJsonRpcMessage)

	// Shutdown the HTTP server
	if t.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := t.httpServer.Shutdown(ctx); err != nil {
			return err
		}
	}

	// Invoke the close handler
	if t.closeHandler != nil {
		t.closeHandler()
	}

	return nil
}

// SetConnectionHandler sets the callback for when a new client connects
func (t *WebSocketTransport) SetConnectionHandler(handler func(clientId string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connectionHandler = handler
}

// SetDisconnectionHandler sets the callback for when a client disconnects
func (t *WebSocketTransport) SetDisconnectionHandler(handler func(clientId string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.disconnectionHandler = handler
}

// SetCloseHandler implements Transport.SetCloseHandler
func (t *WebSocketTransport) SetCloseHandler(handler func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeHandler = handler
}

// SetErrorHandler sets the callback for when an error occurs.
// Note that errors are not necessarily fatal; they are used for reporting any kind of exceptional condition out of band.
func (t *WebSocketTransport) SetErrorHandler(handler func(error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errorHandler = handler
}

// SetMessageHandler sets the callback for when a message (request, notification or response) is received over the connection.
// Partially deserializes the messages to pass a BaseJsonRpcMessage
func (t *WebSocketTransport) SetMessageHandler(handler func(ctx context.Context, message *transport.BaseJsonRpcMessage)) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Simple wrapper - no extra processing needed as client ID is handled through context and requestMeta
	t.messageHandler = handler
}

// cleanupClientChannels removes all response channels associated with a client
func (t *WebSocketTransport) cleanupClientChannels(clientId string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Find and close all channels that start with clientId:
	for key, ch := range t.responseMap {
		if strings.HasPrefix(key, clientId+":") {
			close(ch)
			delete(t.responseMap, key)
			logrus.Debugf("Cleaned up response channel %s for disconnected client\n", key)
		}
	}

	// Clean up request metadata for this client
	for id, meta := range t.requestMeta {
		if meta.clientId == clientId {
			delete(t.requestMeta, id)
			logrus.Debugf("Cleaned up request metadata for client %s, request ID %v\n", clientId, id)
		}
	}
}
