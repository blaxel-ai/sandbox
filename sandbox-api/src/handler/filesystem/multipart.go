package filesystem

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Upload status constants
const (
	UploadStatusPending    = "pending"
	UploadStatusInProgress = "in_progress"
	UploadStatusCompleted  = "completed"
	UploadStatusFailed     = "failed"
)

// MultipartUpload represents an in-progress multipart upload
type MultipartUpload struct {
	UploadID        string                `json:"uploadId" example:"550e8400-e29b-41d4-a716-446655440000"`
	Path            string                `json:"path" example:"/tmp/largefile.dat"`
	Permissions     os.FileMode           `json:"permissions" swaggertype:"integer" example:"420"`
	InitiatedAt     time.Time             `json:"initiatedAt"`
	Parts           map[int]*UploadedPart `json:"parts"`
	Status          string                `json:"status" example:"pending"`
	CompletionError string                `json:"completionError,omitempty" example:""`
	mu              sync.RWMutex          `json:"-" swaggerignore:"true"`
}

// UploadedPart represents a single uploaded part
type UploadedPart struct {
	PartNumber int       `json:"partNumber" example:"1"`
	ETag       string    `json:"etag" example:"5d41402abc4b2a76b9719d911017c592"`
	Size       int64     `json:"size" example:"5242880"`
	UploadedAt time.Time `json:"uploadedAt"`
}

// MultipartManager manages multipart upload sessions
type MultipartManager struct {
	uploads    map[string]*MultipartUpload
	uploadsDir string
	mu         sync.RWMutex
}

// NewMultipartManager creates a new multipart upload manager
func NewMultipartManager(uploadsDir string) *MultipartManager {
	// Ensure uploads directory exists
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return nil
	}

	return &MultipartManager{
		uploads:    make(map[string]*MultipartUpload),
		uploadsDir: uploadsDir,
	}
}

// InitiateUpload creates a new multipart upload session
func (m *MultipartManager) InitiateUpload(path string, permissions os.FileMode) (*MultipartUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	uploadID := uuid.New().String()

	upload := &MultipartUpload{
		UploadID:    uploadID,
		Path:        path,
		Permissions: permissions,
		InitiatedAt: time.Now(),
		Parts:       make(map[int]*UploadedPart),
		Status:      UploadStatusPending,
	}

	m.uploads[uploadID] = upload

	// Create directory for this upload's parts
	uploadDir := filepath.Join(m.uploadsDir, uploadID)
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		delete(m.uploads, uploadID)
		return nil, fmt.Errorf("failed to create upload directory: %w", err)
	}

	// Save metadata
	if err := m.saveMetadata(upload); err != nil {
		_ = os.RemoveAll(uploadDir)
		delete(m.uploads, uploadID)
		return nil, fmt.Errorf("failed to save upload metadata: %w", err)
	}

	return upload, nil
}

// UploadPart uploads a single part of a multipart upload
func (m *MultipartManager) UploadPart(uploadID string, partNumber int, reader io.Reader) (*UploadedPart, error) {
	m.mu.RLock()
	upload, exists := m.uploads[uploadID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}

	if partNumber < 1 || partNumber > 10000 {
		return nil, fmt.Errorf("part number must be between 1 and 10000")
	}

	// Create part file
	partPath := filepath.Join(m.uploadsDir, uploadID, fmt.Sprintf("part-%d", partNumber))
	partFile, err := os.Create(partPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create part file: %w", err)
	}
	defer partFile.Close()

	// Calculate MD5 hash while writing
	hash := md5.New()
	multiWriter := io.MultiWriter(partFile, hash)

	size, err := io.Copy(multiWriter, reader)
	if err != nil {
		_ = os.Remove(partPath)
		return nil, fmt.Errorf("failed to write part: %w", err)
	}

	etag := hex.EncodeToString(hash.Sum(nil))

	part := &UploadedPart{
		PartNumber: partNumber,
		ETag:       etag,
		Size:       size,
		UploadedAt: time.Now(),
	}

	// Update upload metadata
	upload.mu.Lock()
	upload.Parts[partNumber] = part
	upload.mu.Unlock()

	// Save updated metadata
	if err := m.saveMetadata(upload); err != nil {
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}

	return part, nil
}

// CompleteUpload validates parts and spawns a background goroutine to assemble the final file.
// It returns immediately after validation so the HTTP response is not blocked by the copy.
// Clients should poll GetUploadStatus to check when assembly is finished.
func (m *MultipartManager) CompleteUpload(uploadID string, parts []UploadedPart) error {
	m.mu.RLock()
	upload, exists := m.uploads[uploadID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("upload not found: %s", uploadID)
	}

	upload.mu.Lock()

	// Prevent completing an upload that is already in progress or finished
	if upload.Status == UploadStatusInProgress {
		upload.mu.Unlock()
		return fmt.Errorf("upload %s is already being completed", uploadID)
	}
	if upload.Status == UploadStatusCompleted {
		upload.mu.Unlock()
		return fmt.Errorf("upload %s is already completed", uploadID)
	}

	// Validate all parts are present
	for _, part := range parts {
		storedPart, exists := upload.Parts[part.PartNumber]
		if !exists {
			upload.mu.Unlock()
			return fmt.Errorf("part %d not found", part.PartNumber)
		}
		if storedPart.ETag != part.ETag {
			upload.mu.Unlock()
			return fmt.Errorf("etag mismatch for part %d", part.PartNumber)
		}
	}

	// Sort parts by part number
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PartNumber < parts[j].PartNumber
	})

	upload.Status = UploadStatusInProgress
	upload.mu.Unlock()

	// Persist the in_progress status
	_ = m.saveMetadata(upload)

	// Spawn background goroutine for the actual file assembly
	go m.assembleFile(upload, parts)

	return nil
}

// assembleFile concatenates all parts into the final file in the background.
func (m *MultipartManager) assembleFile(upload *MultipartUpload, parts []UploadedPart) {
	uploadID := upload.UploadID

	setFailed := func(err error) {
		upload.mu.Lock()
		upload.Status = UploadStatusFailed
		upload.CompletionError = err.Error()
		upload.mu.Unlock()
		_ = m.saveMetadata(upload)
	}

	// Create parent directories if they don't exist
	dir := filepath.Dir(upload.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		setFailed(fmt.Errorf("failed to create parent directory: %w", err))
		return
	}

	// Create final file
	finalFile, err := os.OpenFile(upload.Path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, upload.Permissions)
	if err != nil {
		setFailed(fmt.Errorf("failed to create final file: %w", err))
		return
	}

	// Concatenate all parts in order
	for _, part := range parts {
		partPath := filepath.Join(m.uploadsDir, uploadID, fmt.Sprintf("part-%d", part.PartNumber))
		partFile, err := os.Open(partPath)
		if err != nil {
			_ = finalFile.Close()
			setFailed(fmt.Errorf("failed to open part %d: %w", part.PartNumber, err))
			return
		}

		if _, err := io.Copy(finalFile, partFile); err != nil {
			_ = partFile.Close()
			_ = finalFile.Close()
			setFailed(fmt.Errorf("failed to copy part %d: %w", part.PartNumber, err))
			return
		}
		_ = partFile.Close()
	}
	_ = finalFile.Close()

	// Mark as completed
	upload.mu.Lock()
	upload.Status = UploadStatusCompleted
	upload.mu.Unlock()
	_ = m.saveMetadata(upload)

	// Clean up upload directory and metadata
	_ = m.cleanupUpload(uploadID)
}

// GetUploadStatus returns the completion status of an upload
func (m *MultipartManager) GetUploadStatus(uploadID string) (string, string, error) {
	m.mu.RLock()
	upload, exists := m.uploads[uploadID]
	m.mu.RUnlock()

	if !exists {
		return "", "", fmt.Errorf("upload not found: %s", uploadID)
	}

	upload.mu.RLock()
	status := upload.Status
	completionError := upload.CompletionError
	upload.mu.RUnlock()

	return status, completionError, nil
}

// cleanupUpload removes the temporary parts directory. The in-memory upload
// record is kept so clients can still poll GetUploadStatus for the terminal
// state. CleanupExpired handles eviction of old records to prevent unbounded
// memory growth.
func (m *MultipartManager) cleanupUpload(uploadID string) error {
	uploadDir := filepath.Join(m.uploadsDir, uploadID)
	if err := os.RemoveAll(uploadDir); err != nil {
		return fmt.Errorf("failed to remove upload directory: %w", err)
	}
	return nil
}

// AbortUpload cancels an upload and cleans up all parts
func (m *MultipartManager) AbortUpload(uploadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	upload, exists := m.uploads[uploadID]
	if !exists {
		return fmt.Errorf("upload not found: %s", uploadID)
	}

	// Prevent aborting an upload that is currently being assembled
	upload.mu.RLock()
	status := upload.Status
	upload.mu.RUnlock()
	if status == UploadStatusInProgress {
		return fmt.Errorf("cannot abort upload %s: assembly is in progress", uploadID)
	}

	// Remove upload directory with all parts
	uploadDir := filepath.Join(m.uploadsDir, uploadID)
	if err := os.RemoveAll(uploadDir); err != nil {
		return fmt.Errorf("failed to remove upload directory: %w", err)
	}

	// Remove from active uploads
	delete(m.uploads, uploadID)

	return nil
}

// ListParts returns all uploaded parts for an upload
func (m *MultipartManager) ListParts(uploadID string) ([]*UploadedPart, error) {
	m.mu.RLock()
	upload, exists := m.uploads[uploadID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}

	upload.mu.RLock()
	defer upload.mu.RUnlock()

	parts := make([]*UploadedPart, 0, len(upload.Parts))
	for _, part := range upload.Parts {
		parts = append(parts, part)
	}

	// Sort by part number
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PartNumber < parts[j].PartNumber
	})

	return parts, nil
}

// GetUpload returns upload metadata
func (m *MultipartManager) GetUpload(uploadID string) (*MultipartUpload, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	upload, exists := m.uploads[uploadID]
	if !exists {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}

	return upload, nil
}

// ListUploads returns all active uploads
func (m *MultipartManager) ListUploads() []*MultipartUpload {
	m.mu.RLock()
	defer m.mu.RUnlock()

	uploads := make([]*MultipartUpload, 0, len(m.uploads))
	for _, upload := range m.uploads {
		uploads = append(uploads, upload)
	}

	return uploads
}

// saveMetadata saves upload metadata to disk
func (m *MultipartManager) saveMetadata(upload *MultipartUpload) error {
	metadataPath := filepath.Join(m.uploadsDir, upload.UploadID, "metadata.json")

	upload.mu.RLock()
	data, err := json.Marshal(upload)
	upload.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metadataPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	return nil
}

// LoadUploads loads all upload metadata from disk
func (m *MultipartManager) LoadUploads() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.uploadsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read uploads directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metadataPath := filepath.Join(m.uploadsDir, entry.Name(), "metadata.json")
		data, err := os.ReadFile(metadataPath)
		if err != nil {
			// Skip corrupted uploads
			continue
		}

		var upload MultipartUpload
		if err := json.Unmarshal(data, &upload); err != nil {
			// Skip corrupted uploads
			continue
		}

		// If the process restarted while assembly was in progress, the goroutine
		// is gone and the upload will never finish. Mark it as failed so clients
		// get a deterministic terminal state instead of polling forever.
		if upload.Status == UploadStatusInProgress {
			upload.Status = UploadStatusFailed
			upload.CompletionError = "assembly interrupted by process restart"
		}

		m.uploads[upload.UploadID] = &upload
	}

	return nil
}

// CleanupExpired removes uploads older than the specified duration
func (m *MultipartManager) CleanupExpired(maxAge time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	var expired []string

	for uploadID, upload := range m.uploads {
		upload.mu.RLock()
		age := now.Sub(upload.InitiatedAt)
		upload.mu.RUnlock()

		if age > maxAge {
			expired = append(expired, uploadID)
		}
	}

	for _, uploadID := range expired {
		uploadDir := filepath.Join(m.uploadsDir, uploadID)
		_ = os.RemoveAll(uploadDir)
		delete(m.uploads, uploadID)
	}

	return nil
}
