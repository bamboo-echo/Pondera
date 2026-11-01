package skill

import (
	"testing"

	"pondera/internal/skill/skillwatch"
	"pondera/internal/testenv"
)

func TestMain(m *testing.M) {
	// Watch tests spawn the helper by re-executing os.Executable(), which here
	// is this test binary. Serve the pipe instead of re-running the suite.
	if skillwatch.MaybeRunHelper() {
		return
	}
	testenv.RunWithIsolatedUserState(m)
}
