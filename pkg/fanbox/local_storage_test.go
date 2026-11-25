package fanbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalStoragePresenceRequiresNonemptyRegularFile(t *testing.T) {
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	asset := Image{ID: "image", Extension: "jpg"}
	storage := &LocalStorage{SaveDir: t.TempDir(), EnableSaveJSON: true, EnableSaveHTML: true}
	for _, output := range []struct {
		name   string
		path   string
		exists func() (bool, error)
	}{
		{"image", storage.makeFileName(post, 0, asset), func() (bool, error) { return storage.Exist(post, 0, asset) }},
		{"text", storage.makeTextFileName(post), func() (bool, error) { return storage.TextExists(post) }},
		{"json", storage.makeJSONFileName(post), func() (bool, error) { return storage.JSONExists(post) }},
		{"html", storage.makeHTMLFileName(post), func() (bool, error) { return storage.HTMLExists(post) }},
	} {
		t.Run(output.name, func(t *testing.T) {
			require.NoError(t, os.MkdirAll(filepath.Dir(output.path), 0755))
			for _, content := range []string{"", "saved"} {
				require.NoError(t, os.WriteFile(output.path, []byte(content), 0644))
				exists, err := output.exists()
				require.NoError(t, err)
				require.Equal(t, content != "", exists)
			}
			require.NoError(t, os.Remove(output.path))
			require.NoError(t, os.Mkdir(output.path, 0755))
			exists, err := output.exists()
			require.NoError(t, err)
			require.False(t, exists)
		})
	}
}

func TestLocalStorageRejectsEmptyExportsWithoutOverwriting(t *testing.T) {
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	storage := &LocalStorage{SaveDir: t.TempDir(), EnableSaveJSON: true, EnableSaveHTML: true}
	for _, output := range []struct {
		name string
		path string
		save func(string) error
	}{
		{"json", storage.makeJSONFileName(post), func(s string) error { return storage.SaveJSON(post, []byte(s)) }},
		{"html", storage.makeHTMLFileName(post), func(s string) error { return storage.SaveHTML(post, s) }},
	} {
		t.Run(output.name, func(t *testing.T) {
			require.Error(t, output.save(""))
			_, err := os.Stat(output.path)
			require.True(t, os.IsNotExist(err))
			require.NoError(t, output.save("good export"))
			require.Error(t, output.save(""))
			data, err := os.ReadFile(output.path)
			require.NoError(t, err)
			require.Equal(t, "good export", string(data))
		})
	}
}

func TestLocalStorageDoesNotCreateTextWithoutContent(t *testing.T) {
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	post.Body.Text = ""
	storage := &LocalStorage{SaveDir: t.TempDir()}
	require.NoError(t, storage.SaveText(post))
	_, err := os.Stat(storage.makeTextFileName(post))
	require.True(t, os.IsNotExist(err))
}
