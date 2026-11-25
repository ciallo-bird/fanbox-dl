package fanbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	downloadStateStatusDownloading = "downloading"
	downloadStateStatusCompleted   = "completed"
	downloadStateStatusFailed      = "failed"
)

type DownloadState struct {
	Version    int    `json:"version"`
	Status     string `json:"status"`
	CreatorID  string `json:"creatorId,omitempty"`
	PostID     string `json:"postId,omitempty"`
	PostTitle  string `json:"postTitle,omitempty"`
	AssetID    string `json:"assetId,omitempty"`
	AssetType  string `json:"assetType,omitempty"`
	URL        string `json:"url,omitempty"`
	FilePath   string `json:"filePath"`
	Bytes      int64  `json:"bytes"`
	TotalBytes int64  `json:"totalBytes,omitempty"`
	UpdatedAt  string `json:"updatedAt"`
	LastError  string `json:"lastError,omitempty"`
}

type DownloadStateMeta struct {
	CreatorID string
	PostID    string
	PostTitle string
	AssetID   string
	AssetType string
	URL       string
}

func stateFilePath(path string) string {
	return path + ".state"
}

func tempFilePath(path string) string {
	return path + ".part"
}

func writeStateFile(path string, state DownloadState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename state file: %w", err)
	}
	return nil
}

func loadStateFile(path string) (DownloadState, error) {
	var state DownloadState
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func hasIncompleteState(path string) bool {
	if _, err := os.Stat(stateFilePath(path)); err == nil {
		return true
	}
	if _, err := os.Stat(tempFilePath(path)); err == nil {
		return true
	}
	return false
}

func tempFileOffset(path string) (int64, bool, error) {
	info, err := os.Stat(tempFilePath(path))
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return info.Size(), true, nil
}

func findStateByURL(root string, targetURL string) (string, int64, bool, error) {
	var foundPath string
	var foundBytes int64

	if _, err := os.Stat(root); os.IsNotExist(err) {
		return "", 0, false, nil
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".state") {
			return nil
		}
		state, err := loadStateFile(path)
		if err != nil {
			return nil
		}
		if state.URL != "" && state.URL == targetURL {
			foundPath = state.FilePath
			foundBytes = state.Bytes
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", 0, false, err
	}
	if foundPath == "" {
		return "", 0, false, nil
	}
	return foundPath, foundBytes, true, nil
}

func isDownloadComplete(path string) (bool, error) {
	if hasIncompleteState(path) {
		return false, nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular() && info.Size() > 0, nil
}

func saveReaderWithState(ctx context.Context, path string, r io.Reader, meta DownloadStateMeta, totalBytes int64, fileMode os.FileMode) error {
	return saveReaderWithStateAt(ctx, path, r, meta, totalBytes, fileMode, 0, false)
}

// totalBytes is the expected final file size, including an appended prefix.
// Nonpositive sizes are unknown; an empty response is never a complete download.
func saveReaderWithStateAt(ctx context.Context, path string, r io.Reader, meta DownloadStateMeta, totalBytes int64, fileMode os.FileMode, startOffset int64, appendMode bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if startOffset < 0 || (!appendMode && startOffset != 0) {
		return fmt.Errorf("invalid download offset: %d", startOffset)
	}
	if fileMode == 0 {
		fileMode = 0664
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}
	statePath := stateFilePath(path)
	tempPath := tempFilePath(path)
	state := DownloadState{
		Version:   1,
		Status:    downloadStateStatusDownloading,
		CreatorID: meta.CreatorID,
		PostID:    meta.PostID,
		PostTitle: meta.PostTitle,
		AssetID:   meta.AssetID,
		AssetType: meta.AssetType,
		URL:       meta.URL,
		FilePath:  path,
		Bytes:     startOffset,
		UpdatedAt: time.Now().Format(time.RFC3339Nano),
	}
	if totalBytes > 0 {
		state.TotalBytes = totalBytes
	}
	if err := writeStateFile(statePath, state); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	written := startOffset
	fail := func(err error) error {
		state.Status = downloadStateStatusFailed
		state.LastError = err.Error()
		state.Bytes = written
		state.UpdatedAt = time.Now().Format(time.RFC3339Nano)
		_ = writeStateFile(statePath, state)
		return err
	}
	var file *os.File
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	buf := make([]byte, 32*1024)
	lastUpdate := time.Now()
	lastBytes := startOffset
	lastProgressLog := lastUpdate
	filename := filepath.Base(path)
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		n, readErr := r.Read(buf)
		if n > 0 {
			if totalBytes > 0 && (written > totalBytes || int64(n) > totalBytes-written) {
				return fail(fmt.Errorf("download exceeds expected size of %d bytes", totalBytes))
			}
			// Do not truncate an existing partial file until new data actually arrives.
			if file == nil {
				flags := os.O_WRONLY
				if appendMode {
					flags |= os.O_APPEND
				} else {
					flags |= os.O_CREATE | os.O_TRUNC
				}
				var err error
				file, err = os.OpenFile(tempPath, flags, fileMode)
				if err != nil {
					return fail(fmt.Errorf("open temp file: %w", err))
				}
				if appendMode {
					info, err := file.Stat()
					if err != nil {
						return fail(fmt.Errorf("stat partial file: %w", err))
					}
					if !info.Mode().IsRegular() || info.Size() != startOffset {
						return fail(fmt.Errorf("partial file size does not match resume offset %d", startOffset))
					}
				}
			}
			nw, err := file.Write(buf[:n])
			written += int64(nw)
			if err != nil {
				return fail(fmt.Errorf("write file: %w", err))
			}
			if nw != n {
				return fail(io.ErrShortWrite)
			}
			now := time.Now()
			if now.Sub(lastUpdate) >= time.Second || written-lastBytes >= 1<<20 {
				state.Bytes = written
				state.UpdatedAt = now.Format(time.RFC3339Nano)
				_ = writeStateFile(statePath, state)
				lastUpdate = now
				lastBytes = written
				if now.Sub(lastProgressLog) >= 2*time.Second {
					logProgress(filename, written, totalBytes)
					lastProgressLog = now
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fail(fmt.Errorf("read stream: %w", readErr))
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if written == startOffset {
		return fail(fmt.Errorf("empty download response"))
	}
	if totalBytes > 0 && written != totalBytes {
		return fail(fmt.Errorf("download has %d bytes, expected %d: %w", written, totalBytes, io.ErrUnexpectedEOF))
	}
	if err := file.Sync(); err != nil {
		return fail(fmt.Errorf("sync file: %w", err))
	}
	if err := file.Close(); err != nil {
		file = nil
		return fail(fmt.Errorf("close file: %w", err))
	}
	file = nil
	// Rename replaces files on supported platforms; never delete a target on failure.
	if err := os.Rename(tempPath, path); err != nil {
		return fail(fmt.Errorf("rename file: %w", err))
	}
	state.Status = downloadStateStatusCompleted
	state.Bytes = written
	state.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	_ = writeStateFile(statePath, state)
	_ = os.Remove(statePath)
	logProgress(filename, written, totalBytes)
	return nil
}

func logProgress(filename string, current int64, total int64) {
	if total > 0 {
		percentage := float64(current) / float64(total) * 100
		slog.Info("Download progress",
			"filename", filename,
			"downloaded", formatBytes(current),
			"total", formatBytes(total),
			"progress", fmt.Sprintf("%.1f%%", percentage))
	} else {
		slog.Info("Download progress",
			"filename", filename,
			"downloaded", formatBytes(current))
	}
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// CleanupOrphanedStates removes orphaned .state and .part files
// Returns the number of cleaned files
func CleanupOrphanedStates(root string) (int, error) {
	cleaned := 0

	if _, err := os.Stat(root); os.IsNotExist(err) {
		return 0, nil
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Clean orphaned .state files (when both target file and part file don't exist)
		if strings.HasSuffix(path, ".state") {
			targetPath := strings.TrimSuffix(path, ".state")
			partPath := targetPath + ".part"
			_, targetExists := os.Stat(targetPath)
			_, partExists := os.Stat(partPath)
			// Only remove if neither target nor part file exists
			if os.IsNotExist(targetExists) && os.IsNotExist(partExists) {
				if err := os.Remove(path); err == nil {
					cleaned++
					slog.Debug("Removed orphaned state file", "path", path)
				}
			}
			return nil
		}

		// Clean orphaned .part files (when no .state file exists)
		if strings.HasSuffix(path, ".part") {
			statePath := strings.TrimSuffix(path, ".part") + ".state"
			if _, err := os.Stat(statePath); os.IsNotExist(err) {
				if err := os.Remove(path); err == nil {
					cleaned++
					slog.Debug("Removed orphaned part file", "path", path)
				}
			}
			return nil
		}

		return nil
	})

	if err != nil {
		return 0, fmt.Errorf("walk directory: %w", err)
	}

	return cleaned, nil
}

// GetIncompleteDownloads returns a list of incomplete downloads
func GetIncompleteDownloads(root string) ([]DownloadState, error) {
	var incomplete []DownloadState

	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".state") {
			return nil
		}

		state, err := loadStateFile(path)
		if err != nil {
			slog.Debug("Failed to load state file", "path", path, "error", err)
			return nil
		}

		if state.Status == downloadStateStatusDownloading {
			incomplete = append(incomplete, state)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	return incomplete, nil
}
