package fanbox

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownloadCompletionRejectsEmptyFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")
	require.NoError(t, os.WriteFile(path, nil, 0644))
	for _, target := range []string{path, dir} {
		complete, err := isDownloadComplete(target)
		require.NoError(t, err)
		require.False(t, complete)
	}
}

func TestSaveRejectsInvalidContentWithoutReplacingFinal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		total int64
		short bool
	}{
		{name: "empty unknown", total: 0},
		{name: "empty known", total: 6},
		{name: "clean EOF before length", body: "abc", total: 6, short: true},
		{name: "body exceeds length", body: "abcdef", total: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "asset.bin")
			require.NoError(t, os.WriteFile(path, []byte("existing valid file"), 0644))
			err := saveReaderWithState(context.Background(), path, strings.NewReader(tc.body), DownloadStateMeta{}, tc.total, 0644)
			require.Error(t, err)
			if tc.short {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			}
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, "existing valid file", string(data))
			state, err := loadStateFile(stateFilePath(path))
			require.NoError(t, err)
			require.Equal(t, downloadStateStatusFailed, state.Status)
			complete, err := isDownloadComplete(path)
			require.NoError(t, err)
			require.False(t, complete)
		})
	}
}

func TestSaveEmptyResumeKeepsExistingPrefixAndFinal(t *testing.T) {
	for _, total := range []int64{0, 6} {
		path := filepath.Join(t.TempDir(), "asset.bin")
		require.NoError(t, os.WriteFile(path, []byte("existing valid file"), 0644))
		require.NoError(t, os.WriteFile(tempFilePath(path), []byte("abc"), 0644))
		err := saveReaderWithStateAt(context.Background(), path, strings.NewReader(""), DownloadStateMeta{}, total, 0644, 3, true)
		require.Error(t, err)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "existing valid file", string(data))
		data, err = os.ReadFile(tempFilePath(path))
		require.NoError(t, err)
		require.Equal(t, "abc", string(data))
	}
}

func TestSaveResumedSizeIncludesPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.bin")
	require.NoError(t, os.WriteFile(tempFilePath(path), []byte("abc"), 0644))
	require.NoError(t, saveReaderWithStateAt(context.Background(), path, strings.NewReader("def"), DownloadStateMeta{}, 6, 0644, 3, true))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "abcdef", string(data))
	complete, err := isDownloadComplete(path)
	require.NoError(t, err)
	require.True(t, complete)
}

func TestSaveUnknownLengthStreamsNonemptyData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.bin")
	require.NoError(t, saveReaderWithState(context.Background(), path, strings.NewReader("payload"), DownloadStateMeta{}, -1, 0644))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "payload", string(data))
}

func TestSaveRejectsChangedResumeOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.bin")
	require.NoError(t, os.WriteFile(tempFilePath(path), []byte("abc"), 0644))
	require.Error(t, saveReaderWithStateAt(context.Background(), path, strings.NewReader("def"), DownloadStateMeta{}, 6, 0644, 2, true))
	data, err := os.ReadFile(tempFilePath(path))
	require.NoError(t, err)
	require.Equal(t, "abc", string(data))
	require.NoFileExists(t, path)
}

func TestSaveRenameFailureDoesNotDeleteTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing-directory")
	require.NoError(t, os.Mkdir(path, 0755))
	require.Error(t, saveReaderWithState(context.Background(), path, strings.NewReader("payload"), DownloadStateMeta{}, 7, 0644))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	data, err := os.ReadFile(tempFilePath(path))
	require.NoError(t, err)
	require.Equal(t, "payload", string(data))
}
