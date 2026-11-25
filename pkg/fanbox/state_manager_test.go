package fanbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStateManagerMutationsPersistWithoutDeadlock(t *testing.T) {
	dir := t.TempDir()
	state, err := NewStateManager(dir)
	require.NoError(t, err)
	creator := SupportingCreator{CreatorID: "creator", Name: "Creator", Fee: 500}
	done := make(chan error, 1)
	go func() {
		if err := state.UpdateLastPostID("creator", 30); err != nil {
			done <- err
			return
		}
		if err := state.AddSupportingCreator("account", creator); err != nil {
			done <- err
			return
		}
		done <- state.UpdateSupportingList("account", []SupportingCreator{creator})
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("state mutation deadlocked while saving")
	}
	reloaded, err := NewStateManager(dir)
	require.NoError(t, err)
	id, ok := reloaded.GetLastPostID("creator")
	require.True(t, ok)
	require.EqualValues(t, 30, id)
	require.Equal(t, []SupportingCreator{creator}, reloaded.GetSupportingList()["account"])
}
