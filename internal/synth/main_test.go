package synth

import (
	"os"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/lsptest"
)

func TestMain(m *testing.M) { os.Exit(lsptest.RunWithoutManagedDaemon(m)) }
