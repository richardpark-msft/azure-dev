// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"

	"github.com/coder/acp-go-sdk"
)

// ACPMessageLogger provides concurrency-safe JSONL logging for ACP messages.
// Each message is converted to a map before serialization to ensure all fields
// are captured regardless of ACP library's custom unmarshalers.
type ACPMessageLogger struct {
	mu     sync.Mutex
	writer *bufio.Writer
	file   *os.File
}

// NewACPMessageLogger creates a new logger that appends JSONL entries to the specified file.
func NewACPMessageLogger(logPath string) (*ACPMessageLogger, error) {
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	return &ACPMessageLogger{
		writer: bufio.NewWriter(file),
		file:   file,
	}, nil
}

// Close flushes any buffered data and closes the underlying file.
func (l *ACPMessageLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.writer.Flush(); err != nil {
		return err
	}
	return l.file.Close()
}

// LogRequestPermission logs an ACP RequestPermissionRequest with source "RequestPermission".
func (l *ACPMessageLogger) LogRequestPermission(params acp.RequestPermissionRequest) {
	l.Log("RequestPermission", params)
}

// LogSessionUpdate logs an ACP SessionNotification with source "SessionUpdate".
func (l *ACPMessageLogger) LogSessionUpdate(params acp.SessionNotification) {
	l.Log("SessionUpdate", params)
}

// LogCustomMessage lets you inject your own message, in case you want to note some external event to correlate
// against other events in the same file.
func (l *ACPMessageLogger) LogCustomMessage(message string) {
	l.Log("CustomMessage", message)
}

// Log writes a JSONL entry with the given source and data.
// If the data cannot be serialized, an error entry is written instead.
func (l *ACPMessageLogger) Log(source string, data any) {
	if l == nil {
		return
	}

	if v, ok := data.(string); ok {
		data = map[string]any{
			"source": "custom",
			"message": v,
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	var entry map[string]any
	dataMap, err := toMap(data)
	if err != nil {
		entry = map[string]any{
			"source":    source,
			"error":     err.Error(),
		}
	} else {
		entry = map[string]any{
			"source":    source,
			"data":      dataMap,
		}
	}

	jsonData, err := json.Marshal(entry)
	if err != nil {
		// Last resort: write a minimal error entry
		jsonData = []byte(`{"source":"` + source + `","error":"failed to marshal entry"}`)
	}

	l.writeLine(jsonData)
}

func (l *ACPMessageLogger) writeLine(data []byte) {
	_, _ = l.writer.Write(data)
	_, _ = l.writer.WriteString("\n")
	_ = l.writer.Flush()
	_ = l.file.Sync()
}

// toMap converts any struct to a map[string]any by marshaling to JSON and back.
// This ensures all JSON-serializable fields are captured.
func toMap(data any) (map[string]any, error) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, err
	}

	return result, nil
}
