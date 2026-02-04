package api

import (
	"fmt"
	"math"
	"time"

	"github.com/gin-gonic/gin"
)

// processingTimeWriter wraps gin.ResponseWriter to add processing time header
type processingTimeWriter struct {
	gin.ResponseWriter
	startTime     time.Time
	headerWritten bool
}

// writeServerTimingHeader writes the Server-Timing header if not already written
func (w *processingTimeWriter) writeServerTimingHeader() {
	if !w.headerWritten {
		elapsed := time.Since(w.startTime)
		latency := float64(elapsed.Nanoseconds()) / 1000000.0
		// Use Server-Timing header for standard browser DevTools integration
		w.Header().Set("Server-Timing", fmt.Sprintf("total;dur=%.2f;desc=\"Processing time of the Sandbox API request\"", latency))
		w.headerWritten = true
	}
}

func (w *processingTimeWriter) WriteHeader(statusCode int) {
	w.writeServerTimingHeader()
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *processingTimeWriter) Write(data []byte) (int, error) {
	w.writeServerTimingHeader()
	return w.ResponseWriter.Write(data)
}

func (w *processingTimeWriter) WriteHeaderNow() {
	w.writeServerTimingHeader()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *processingTimeWriter) Flush() {
	w.writeServerTimingHeader()
	w.ResponseWriter.Flush()
}

func processingTimeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		// Wrap the response writer to intercept all header-writing methods
		ptw := &processingTimeWriter{
			ResponseWriter: c.Writer,
			startTime:      start,
			headerWritten:  false,
		}
		c.Writer = ptw

		c.Next()

		// Also store in context for backward compatibility
		stop := time.Since(start)
		latency := float64(stop.Nanoseconds()) / 1000000.0
		c.Set("processingTime", latency)
	}
}
